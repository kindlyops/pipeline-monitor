package main

import (
	"context"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/codebuild"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

type codeBuildLogInfo struct {
	groupName  string
	streamName string
	deepLink   string
}

type buildDetails struct {
	owner      string
	repo       string
	prID       int
	commitID   string
	logInfo    codeBuildLogInfo
	body       string
	commentTag string
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

func getCodeBuildLog(sess client.ConfigProvider, info codeBuildLogInfo, limit int) (string, error) {
	svc := cloudwatchlogs.New(sess)
	resp, err := svc.GetLogEvents(&cloudwatchlogs.GetLogEventsInput{
		Limit:         aws.Int64(int64(limit)),
		LogGroupName:  aws.String(info.groupName),
		LogStreamName: aws.String(info.streamName),
	})

	if err != nil {
		return "", err
	}

	var body strings.Builder

	for _, event := range resp.Events {
		body.WriteString(*event.Message)
	}

	return body.String(), nil
}

func getCodeBuildDetails(buildID string, limit int, projectName string) (buildDetails, error) {
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))

	// change this to use an interface so that this can be mocked/tested
	// https://docs.aws.amazon.com/sdk-for-go/api/service/codepipeline/codepipelineiface/
	svc := codebuild.New(sess)

	apiInput := &codebuild.BatchGetBuildsInput{
		Ids: []*string{aws.String(buildID)},
	}

	var data buildDetails

	result, err := svc.BatchGetBuilds(apiInput)
	if err != nil || len(result.Builds) != 1 {
		return data, fmt.Errorf("unexpected %d results for build-id: %s", len(result.Builds), buildID)
	}

	build := result.Builds[0]
	if *build.Source.Type != "GITHUB" {
		return data, fmt.Errorf("this only works with Source.Type == GITHUB, found Source.Type == %s", *build.Source.Type)
	}

	info, err := parseRepoURL(*build.Source.Location)
	if err != nil {
		return data, fmt.Errorf("could not parse source location data: %s", err)
	}

	data.commitID = *build.ResolvedSourceVersion
	data.owner = info.owner
	data.repo = info.repo
	data.logInfo.groupName = *build.Logs.GroupName
	data.logInfo.streamName = *build.Logs.StreamName
	data.logInfo.deepLink = *build.Logs.DeepLink
	data.prID, err = parsePrID(*build.SourceVersion)

	if err != nil {
		return data, fmt.Errorf("error parsing PR id: %s", err)
	}

	data.commentTag = "PIPELINE_MONITOR_GENERATED_LOG_COMMENT_" + strings.ToUpper(projectName)
	logBody, err := getCodeBuildLog(sess, data.logInfo, limit)

	if err != nil {
		return data, fmt.Errorf("error retrieving codebuild logs for %s: %s", *build.Logs.DeepLink, err)
	}

	commentData := map[string]string{
		"body":           logBody,
		"commentTag":     data.commentTag,
		"deepLink":       data.logInfo.deepLink,
		"limit":          strconv.Itoa(limit),
		"projectName":    projectName,
		"tripleBacktick": "```",
	}
	commentHiddenTag := fmt.Sprintf("<!-- %s -->\n", data.commentTag)
	commentTemplate := `
## First {{.limit}} lines of {{.projectName}} latest build log
<details>
  <summary>Click to expand the latest build log!</summary>

  ## Link to [original cloudwatch log]({{.deepLink}})
{{.tripleBacktick}}
{{.body}}
{{.tripleBacktick}}
</details>
`

	var body strings.Builder

	t := template.Must(template.New("t1").Parse(commentTemplate))
	err = t.Execute(&body, commentData)

	if err != nil {
		return data, fmt.Errorf("error formatting comment template: %s", err)
	}
	// commentHiddenTag must be separate because go templates strip HTML comments
	data.body = commentHiddenTag + body.String()

	return data, err
}

func parsePrID(sourceVersion string) (int, error) {
	// grab the github repo and owner from the CodeBuild SourceVersion,
	// which looks like "pr/39"
	// this only works if CodeBuild is configured to build on event types
	// PULL_REQUEST_UPDATED
	// PULL_REQUEST_CREATED
	// PULL_REQUEST_REOPENED
	// the event notifications from event PUSH only contain a git revision hash
	var prMatcher = regexp.MustCompile(`pr/(?P<id>[\d]+)$`)

	var expectedMatches = prMatcher.NumSubexp() + 1

	match := prMatcher.FindStringSubmatch(sourceVersion)

	if len(match) != expectedMatches {
		err := fmt.Errorf("failed to parse SourceVersion, expected %v matches for %s and got %v",
			expectedMatches, sourceVersion, len(match))
		return -1, err
	}

	number, err := strconv.Atoi(match[1])
	if err != nil {
		return -1, err
	}

	return number, nil
}

func upsertGitHubLogComment(details *buildDetails, token string) error {
	// guidance on auth from https://github.com/google/go-github#authentication
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	gh := github.NewClient(tc)

	// iterate PR comments
	opt := &github.IssueListCommentsOptions{Sort: "updated", Direction: "desc"}
	comments, _, err := gh.Issues.ListComments(ctx, details.owner, details.repo, details.prID, opt)
	if err != nil {
		return err
	}

	// delete old comment, we will post a new one
	for _, comment := range comments {
		if strings.Contains(*comment.Body, details.commentTag) {
			_, _ = gh.Issues.DeleteComment(ctx, details.owner, details.repo, *comment.ID)
		}
	}

	comment := &github.IssueComment{Body: &details.body}
	_, _, err = gh.Issues.CreateComment(ctx, details.owner, details.repo, details.prID, comment)

	return err
}
