package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRevisionURL(t *testing.T) {
	t.Parallel()

	testURL := "https://github.com/owner_name/repo-name/commit/8873423234re34ea1daewerwe93f92d1557a7b9b"

	result, err := parseRevisionURL(testURL)
	assert.NoError(t, err)
	assert.EqualValues(t, result.owner, "owner_name")
	assert.EqualValues(t, result.repo, "repo-name")
	assert.EqualValues(t, result.commit, "8873423234re34ea1daewerwe93f92d1557a7b9b")
}
