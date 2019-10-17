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
