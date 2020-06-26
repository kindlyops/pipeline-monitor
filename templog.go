package main

import (
	"fmt"
	"os"
)

func main() {
	var err error

	fmt.Println("Running temp log diagnostic")

	buildID := "pipeline-monitor-lint:b1ce3a16-c26e-40c8-8af1-8b9da5b1b51a"
	maxLogLines := 10000
	details, err := getCodeBuildDetails(buildID, maxLogLines, "fake-project-name")
	if err != nil {
		fmt.Printf("Error getting code build details: %s", err.Error())
		os.Exit(1)
	}

	token := os.Getenv("GITHUB_TOKEN")
	err = upsertGitHubLogComment(&details, token)
	if err != nil {
		fmt.Printf("Error adding comment: %s", err.Error())
	}
}
