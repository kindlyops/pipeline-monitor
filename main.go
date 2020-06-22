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
	"github.com/aws/aws-sdk-go/service/codebuild"
	"github.com/aws/aws-sdk-go/service/codepipeline"
	"github.com/aws/aws-sdk-go/service/secretsmanager"

	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// load the GitHub Oauth token when the lambda is loaded, not on every execution
// this reduces the call volume into SecretsManager
var gitHubToken string

type secretToken struct {
	Token string `json:"token"`
}

func getGitHubToken() (string, error) {
	secretName := os.Getenv("SECRETSMANAGER_GITHUBTOKEN_NAME")
	if secretName == "" {
		err := fmt.Errorf("couldn't find SECRETSMANAGER_GITHUBTOKEN_NAME in environment")
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

func parseRepoURL(revisionURL string) (revisionInfo, error) {
	// grab the github repo and owner from the repo URL, which looks like
	// "https://github.com/owner/repo.git"
	u, err := url.Parse(revisionURL)
	if err != nil {
		err = fmt.Errorf("failed to parse revision URL: %s", err.Error())
		return revisionInfo{}, err
	}

	var commitMatcher = regexp.MustCompile(`/(?P<owner>[\w-]+)/(?P<repo>[\w-]+)\.git$`)

	var expectedMatches = commitMatcher.NumSubexp() + 1

	match := commitMatcher.FindStringSubmatch(u.Path)

	if len(match) != expectedMatches {
		err = fmt.Errorf("failed to parse revision URL, expected %v matches for %s and got %v",
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
		owner: matches["owner"],
		repo:  matches["repo"],
	}

	return info, nil
}

func parseRevisionURL(revisionURL string) (revisionInfo, error) {
	// grab the github repo and owner from the revision URL, which looks like
	// "https://github.com/owner/repo/commit/8873423234re34ea1daewerwe93f92d1557a7b9b"
	u, err := url.Parse(revisionURL)
	if err != nil {
		err = fmt.Errorf("failed to parse revision URL: %s", err.Error())
		return revisionInfo{}, err
	}

	var commitMatcher = regexp.MustCompile(`/(?P<owner>[\w-]+)/(?P<repo>[\w-]+)/commit/(?P<commit>\w+)$`)

	var expectedMatches = commitMatcher.NumSubexp() + 1

	match := commitMatcher.FindStringSubmatch(u.Path)

	if len(match) != expectedMatches {
		err = fmt.Errorf("failed to parse revision URL, expected %v matches for %s and got %v",
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

	// change this to use an interface so that this can be mocked/tested
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
		err = fmt.Errorf("did not expect multiple CodePipeline artifacts, got: %v", count)
		return nil, err
	}

	artifact := artifacts[0]

	info, err := parseRevisionURL(*artifact.RevisionUrl)

	if err != nil || info.commit != *artifact.RevisionId {
		err = fmt.Errorf("failed to parse revision URL: %s", err.Error())
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

func updateGitHubStatus(status *statusInfo) error {
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

	_, _, err := client.Repositories.CreateStatus(
		context.Background(),
		status.owner,
		status.repo,
		status.commitID,
		repoStatus,
	)

	if err != nil {
		err = fmt.Errorf("error creating GitHub commit status: %s", err)
	}

	return err
}

func processCodePipelineNotification(request events.CloudWatchEvent, detail map[string]interface{}) error {

	// we process Action execution state changes so that we can get granular
	// status updates on deploys of individual services or stacks
	log.Printf("Processing %s", request.DetailType)

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
	}

	log.Printf("Processing the %s stage for %s", detail["stage"], pipelineStatusPage)

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

	err = updateGitHubStatus(&commitStatus)

	if err != nil {
		log.Printf("Error updating GitHub commit status: %s", err.Error())
	}

	return err
}

type codeBuildLogInfo struct {
	groupName  string
	streamName string
	deepLink   string
}

// type sourceInfo struct {

// }

type buildDetails struct {
	owner    string
	repo     string
	prID     string
	commitID string
	logInfo  codeBuildLogInfo
	body     string
}

func parsePrId(sourceVersion string) (string, error) {
	// grab the github repo and owner from the CodeBuild SourceVersion,
	// which looks like "pr/39"
	var prMatcher = regexp.MustCompile(`pr/(?P<id>[\d]+)$`)

	var expectedMatches = prMatcher.NumSubexp() + 1

	match := prMatcher.FindStringSubmatch(sourceVersion)

	if len(match) != expectedMatches {
		err := fmt.Errorf("failed to parse SourceVersion, expected %v matches for %s and got %v",
			expectedMatches, sourceVersion, len(match))
		return "", err
	}

	return match[1], nil
}

func getCodeBuildLog(info codeBuildLogInfo) (string, error) {
	// TODO
	return "TODO LOG BODY", nil
}

func getCodeBuildDetails(buildId string) (buildDetails, error) {
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))

	// change this to use an interface so that this can be mocked/tested
	// https://docs.aws.amazon.com/sdk-for-go/api/service/codepipeline/codepipelineiface/
	svc := codebuild.New(sess)

	apiInput := &codebuild.BatchGetBuildsInput{
		Ids: []*string{aws.String(buildId)},
	}

	var data buildDetails

	result, err := svc.BatchGetBuilds(apiInput)
	if len(result.Builds) != 1 {
		return data, fmt.Errorf("unexpected %d results for build-id: %s", len(result.Builds), buildId)
	}
	build := result.Builds[0]
	if *build.Source.Type != "GITHUB" {
		return data, fmt.Errorf("this only works with Source.Type == GITHUB, found Source.Type == %s", *build.Source.Type)
	}

	data.commitID = *build.ResolvedSourceVersion
	data.repo = strings.TrimSuffix(*build.Source.Location, ".git")
	data.logInfo.groupName = *build.Logs.GroupName
	data.logInfo.streamName = *build.Logs.StreamName
	data.logInfo.deepLink = *build.Logs.DeepLink
	data.prID, err = parsePrId(*build.SourceVersion)

	commentTemplate := `
<!--
PIPELINE_MONITOR_GENERATED_LOG_COMMENT
-->
<details>
  <summary>Click to expand the latest build log!</summary>

  ## Link to original cloudwatch log
  %v

  ## First 64K of build log

  %v
</details>

`
	logBody, err := getCodeBuildLog(data.logInfo)

	data.body = fmt.Sprintf(commentTemplate, data.logInfo.deepLink, logBody)

	return data, err
}

func updateGitHubLogComment(details *buildDetails) error {
	// guidance on auth from https://github.com/google/go-github#authentication
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: gitHubToken},
	)
	tc := oauth2.NewClient(ctx, ts)
	_ = github.NewClient(tc)

	// _, _, err := client.Issues.CreateComment(
	// 	context.Background(),
	// )

	// if err != nil {
	// 	err = fmt.Errorf("error creating GitHub commit status: %s", err)
	// }

	return nil
}

func processCodeBuildNotification(request events.CloudWatchEvent, detail map[string]interface{}) error {
	currentPhase := detail["current-phase"].(string)
	if currentPhase != "COMPLETED" {
		log.Printf("Ignoring build notification for phase %s", currentPhase)
		return nil
	}

	// the CodeBuild event notifications have inconsistent information
	// (data fields only contain PR ID on retry, not on webhook-triggered build)
	// call into the CodeBuild API to get sufficient info
	buildId := detail["build-id"].(string)
	details, err := getCodeBuildDetails(buildId)
	log.Printf("codebuildDetails: %v", details)
	log.Printf("full event is %s\n", request.Detail)

	return err
}

// HandleRequest is the main entry point for the lambda processing.
func HandleRequest(ctx context.Context, request events.CloudWatchEvent) error {
	// unmarshal detail
	// https://docs.aws.amazon.com/AmazonCloudWatch/latest/events/EventTypes.html#codepipeline_event_type
	var holder interface{}

	err := json.Unmarshal(request.Detail, &holder)

	if err != nil {
		return fmt.Errorf("unable to unmarshal Lambda Event detail: %s\n", err)
	}

	detail := holder.(map[string]interface{})

	switch request.DetailType {
	case "CodePipeline Action Execution State Change":
		err = processCodePipelineNotification(request, detail)
	case "CodeBuild Build State Change": // these come from PR builds
		err = processCodeBuildNotification(request, detail)
	default:
		log.Printf("Ignoring %s\n", request.DetailType)
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
