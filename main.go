package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/codepipeline"
	"github.com/aws/aws-sdk-go/service/secretsmanager"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// CodePipelineNotificationDetail contains state detail about CodePipeline jobs
type CodePipelineNotificationDetail struct {
	Pipeline string `json:"pipeline"`
	State    string `json:"state"`
}

// load the GitHub Oauth token when the lambda is loaded, not on every execution
// this reduces the call volume into SecretsManager
var (
	gitHubToken string
	state       string
)

type secretToken struct {
	Token string `json:"token"`
}

func getGitHubToken() (string, error) {

	secretName := os.Getenv("SECRETSMANAGER_GITHUBTOKEN_NAME")
	if secretName == "" {
		err := fmt.Errorf("Couln't find SECRETSMANAGER_GITHUBTOKEN_NAME in environment.")
		return "", err
	}

	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String(endpoints.UsWest2RegionID),
	}))

	svc := secretsmanager.New(sess)
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}

	result, err := svc.GetSecretValue(input)

	if err != nil {
		err = fmt.Errorf("unable to retrieve GitHub auth token: %s", err.Error())
		return "", err
	}

	var token secretToken
	err = json.Unmarshal([]byte(*result.SecretString), &token)
	if err != nil {
		err = fmt.Errorf("unable to unmarshal secret token: %v", err.Error())
		return "", err
	}

	return token.Token, nil
}

type executionDetails struct {
	executionID  string
	pipelineName string
}

type revisionInfo struct {
	owner  string
	repo   string
	commit string
}

func parseRevisionURL(revisionURL string) (revisionInfo, error) {
	// grab the github repo and owner from the revision URL, which looks like
	// "https://github.com/owner/repo/commit/8873423234re34ea1daewerwe93f92d1557a7b9b"
	u, err := url.Parse(revisionURL)
	if err != nil {
		err = fmt.Errorf("Failed to parse revision URL: %s", err.Error())
		return revisionInfo{}, err
	}

	var commitMatcher = regexp.MustCompile(`/(?P<owner>[\w-]+)/(?P<repo>[\w-]+)/commit/(?P<commit>\w+)$`)
	var expectedMatches = commitMatcher.NumSubexp() + 1

	match := commitMatcher.FindStringSubmatch(u.Path)

	if len(match) != expectedMatches {
		err = fmt.Errorf("Failed to parse revision URL, expected %v matches for %s and got %v.",
			expectedMatches, u.Path, len(match))
		return revisionInfo{}, err
	}
	matches := make(map[string]string)
	for i, name := range commitMatcher.SubexpNames() {
		if i != 0 && name != "" {
			matches[name] = match[i]
		}
	}

	info := revisionInfo{
		owner:  matches["owner"],
		repo:   matches["repo"],
		commit: matches["commit"],
	}

	return info, nil
}

// convert from CodePipeline ActionExecution status to GitHub commit status
// https://developer.github.com/v3/repos/statuses/
// https://docs.aws.amazon.com/codepipeline/latest/APIReference/API_ActionExecution.html
func translateStatus(status string) string {
	var actionState string
	switch status {
	case "STARTED":
		actionState = "pending"
	case "SUCCEEDED":
		actionState = "success"
	case "FAILED":
		actionState = "failure"
	default:
		actionState = "error"
	}
	return actionState
}

func getRevisionID(input executionDetails) (*revisionInfo, error) {

	sess := session.Must(session.NewSession())

	// TODO: use an interface so that this can be mocked/tested
	// https://docs.aws.amazon.com/sdk-for-go/api/service/codepipeline/codepipelineiface/
	svc := codepipeline.New(sess)

	apiInput := &codepipeline.GetPipelineExecutionInput{
		PipelineExecutionId: aws.String(input.executionID),
		PipelineName:        aws.String(input.pipelineName),
	}

	result, err := svc.GetPipelineExecution(apiInput)

	if err != nil {
		err = fmt.Errorf("unable to retrieve Pipeline execution state: %s", err.Error())
		return nil, err
	}

	artifacts := result.PipelineExecution.ArtifactRevisions

	if count := len(artifacts); count > 1 {
		err = fmt.Errorf("Did not expect multiple CodePipeline artifacts, got: %v", count)
		return nil, err
	}
	artifact := artifacts[0]

	info, err := parseRevisionURL(*artifact.RevisionUrl)

	if err != nil || info.commit != *artifact.RevisionId {
		err = fmt.Errorf("Failed to parse revision URL: %s", err.Error())
		return nil, err
	}

	return &info, nil
}

type statusInfo struct {
	owner       string
	repo        string
	commitID    string
	description string
	url         string
	state       string
	label       string
}

func updateGitHubStatus(status statusInfo) error {

	// guidance on auth from https://github.com/google/go-github#authentication
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: gitHubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	repoStatus := &github.RepoStatus{}
	repoStatus.State = &status.state
	repoStatus.Context = &status.label
	repoStatus.Description = &status.description
	repoStatus.TargetURL = &status.url

	repoStatus, _, err := client.Repositories.CreateStatus(
		context.Background(),
		status.owner,
		status.repo,
		status.commitID,
		repoStatus,
	)

	if err != nil {
		err = fmt.Errorf("Error creating GitHub commit status: %s", err)
	}
	return err
}

// HandleRequest is the main entry point for the lambda processing.
func HandleRequest(ctx context.Context, request events.CloudWatchEvent) error {

	// unmarshal detail
	// https://docs.aws.amazon.com/AmazonCloudWatch/latest/events/EventTypes.html#codepipeline_event_type
	var holder interface{}
	err := json.Unmarshal(request.Detail, &holder)
	if err != nil {
		return fmt.Errorf("unable to unmarshal CodePipelineEvent detail: %s", err)
	}
	detail := holder.(map[string]interface{})

	// we process Action execution state changes so that we can get granular
	// status updates on deploys of individual services or stacks
	var actionExecutionDetail = "CodePipeline Action Execution State Change"
	if request.DetailType != actionExecutionDetail {
		log.Printf("Ignoring %s", request.DetailType)
		return nil
	} else {
		log.Printf("Processing %s", request.DetailType)
	}

	details := executionDetails{
		pipelineName: detail["pipeline"].(string),
		executionID:  detail["execution-id"].(string),
	}

	pipelineStatusPage := fmt.Sprintf(
		"https://%s.console.aws.amazon.com/codesuite/codepipeline/pipelines/%s/executions/%s/timeline",
		request.Region,
		details.pipelineName,
		details.executionID)

	// ignore the Source stage (this is the github trigger)
	if detail["stage"] == "Source" {
		log.Printf("Ignoring the Source stage for %s", pipelineStatusPage)
		return nil
	} else {
		log.Printf("Processing the %s stage for %s", detail["stage"], pipelineStatusPage)
	}

	revisionInfo, err := getRevisionID(details)

	if err != nil {
		log.Printf("Error getting revision ID for %s: %s", pipelineStatusPage, err.Error())
		return nil
	}

	actionState := translateStatus(detail["state"].(string))
	action := detail["action"].(string)
	statusLabel := action
	statusDescription := fmt.Sprintf("%s stage executing in %s", detail["stage"], detail["region"])

	if strings.Contains(action, "-") {
		// if the action name has a -, split up the label to make it a bit easier
		// to read in the GitHub web UI
		parts := strings.Split(action, "-")
		statusLabel = fmt.Sprintf("%s for %s", parts[0], parts[1])
	}

	commitStatus := statusInfo{
		commitID:    revisionInfo.commit,
		owner:       revisionInfo.owner,
		repo:        revisionInfo.repo,
		url:         pipelineStatusPage,
		label:       statusLabel,
		state:       actionState,
		description: statusDescription,
	}

	err = updateGitHubStatus(commitStatus)

	if err != nil {
		log.Printf("Error updating GitHub commit status: %s", err.Error())
	}

	return err
}

func main() {
	// initialize secrets on lambda boot, not on every invocation
	var err error
	gitHubToken, err = getGitHubToken()

	if err != nil {
		log.Printf("Error loading github access token: %s", err.Error())
		os.Exit(1)
	}

	lambda.Start(HandleRequest)
}
