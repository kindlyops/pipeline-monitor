package main

import (
	"context"
	"fmt"
	"html/template"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
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
	prID       string
	commitID   string
	logInfo    codeBuildLogInfo
	body       string
	commentTag string
}

func getCodeBuildLog(sess *session.Session, info codeBuildLogInfo, limit int) (string, error) {
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

func getCodeBuildDetails(buildId string, limit int, projectName string) (buildDetails, error) {
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
	if err != nil || len(result.Builds) != 1 {
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
	data.commentTag = "PIPELINE_MONITOR_GENERATED_LOG_COMMENT_" + strings.ToUpper(projectName)
	logBody, err := getCodeBuildLog(sess, data.logInfo, limit)
	if err != nil {
		return data, fmt.Errorf("error retrieving codebuild logs for %s: %s", *build.Logs.DeepLink, err)
	}
	var commentData = map[string]string{
		"body":           logBody,
		"commentTag":     data.commentTag,
		"deepLink":       data.logInfo.deepLink,
		"limit":          strconv.Itoa(limit),
		"projectName":    projectName,
		"tripleBacktick": "```",
	}
	commentTemplate := `
<!--
{{.commentTag}}
-->
## First {{.limit}} lines of {{.projectName}} latest build log
<details>
  <summary>Click to expand the latest build log!</summary>

  ## Link to [original cloudwatch log]({{.deepLink}})
{{.tripleBacktick}}
{{.body}}
{{.tripleBacktick}}
</details>
`
	t := template.Must(template.New("t1").Parse(commentTemplate))
	var body strings.Builder
	err = t.Execute(&body, commentData)
	if err != nil {
		return data, fmt.Errorf("error formatting comment template: %s", err)
	}
	data.body = body.String()

	return data, err
}

func parsePrId(sourceVersion string) (string, error) {
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
		return "", err
	}

	return match[1], nil
}

func upsertGitHubLogComment(details *buildDetails, token string) error {
	// guidance on auth from https://github.com/google/go-github#authentication
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	_ = github.NewClient(tc)

	// iterate PR comments

	// Found comment with tag?
	// update comment
	// else insert new comment
	// _, _, err := client.Issues.CreateComment(
	// 	context.Background(),
	// )

	// if err != nil {
	// 	err = fmt.Errorf("error creating GitHub commit status: %s", err)
	// }

	return nil
}
