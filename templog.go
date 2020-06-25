package main

import (
	"fmt"
	"os"
)

func main() {
	var err error

	fmt.Println("Running temp log diagnostic")

	buildID := "pipeline-monitor-lint:e6632df9-932d-4f06-9fc5-3723a132361a"
	maxLogLines := 10000
	details, err := getCodeBuildDetails(buildID, maxLogLines, "fake-project-name")
	if err != nil {
		fmt.Printf("Error getting code build details: %s", err.Error())
		os.Exit(1)
	}

	fmt.Printf(details.body)
}
