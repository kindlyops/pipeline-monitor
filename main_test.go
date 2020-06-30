package main

import (
	"testing"
)

func TestParseRevisionURL(t *testing.T) {
	t.Parallel()

	testURL := "https://github.com/owner_name/repo-name/commit/8873423234re34ea1daewerwe93f92d1557a7b9b"

	result, err := parseRevisionURL(testURL)
	if err != nil {
		t.Error("error in parseRevisionURL", err)
	}

	if result.owner != "owner_name" {
		t.Error("got wrong owner", result.owner)
	}

	if result.repo != "repo-name" {
		t.Error("got wrong repo", result, result.repo)
	}

	if result.commit != "8873423234re34ea1daewerwe93f92d1557a7b9b" {
		t.Error("got wrong commit", result.commit)
	}
}

// Sample event for CodeBuild status change
// {
//   "version": "0",
//   "id": "bfdc1220-60ff-44ad-bfa7-3b6e6ba3b2d0",
//   "detail-type": "CodeBuild Build State Change",
//   "source": "aws.codebuild",
//   "account": "123456789012",
//   "time": "2017-07-12T00:42:28Z",
//   "region": "us-east-1",
//   "resources": [
//     "arn:aws:codebuild:us-east-1:123456789012:build/SampleProjectName:ed6aa685-0d76-41da-a7f5-6d8760f41f55"
//   ],
//   "detail": {
//     "build-status": "SUCCEEDED",
//     "project-name": "SampleProjectName",
//     "build-id": "arn:aws:codebuild:us-east-1:123456789012:build/SampleProjectName:ed6aa685-0d76-41da-a7f5-6d8760f41f55",
//     "current-phase": "COMPLETED",
//     "current-phase-context": "[]",
//     "version": "1"
//   }
// }

//

func TestParseRepoURL(t *testing.T) {
	t.Parallel()

	testURL := "https://github.com/owner_name/repo-name.git"

	result, err := parseRepoURL(testURL)
	if err != nil {
		t.Error("error in parseRevisionURL", err)
	}

	if result.owner != "owner_name" {
		t.Error("got wrong owner", result.owner)
	}

	if result.repo != "repo-name" {
		t.Error("got wrong repo", result, result.repo)
	}

	if result.commit != "" {
		t.Error("got wrong commit", result.commit)
	}
}

func TestParsePrId(t *testing.T) {
	t.Parallel()

	sourceVersion := "pr/39"
	result, err := parsePrID(sourceVersion)
	if err != nil {
		t.Error("error in parsePrID", err)
	}

	if result != 39 {
		t.Error("got wrong id", result)
	}
}
