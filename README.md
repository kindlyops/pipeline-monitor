# pipeline-monitor
lambda function to monitor AWS CodePipeline and annotate GitHub commits with status of pipeline action executions

## build and test

    bazel test //...

## updating dependencies

    go mod update
    go mod tidy
    go mod vendor
    bazel run //:gazelle -- update-repos -from_file=go.mod

