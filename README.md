# pipeline-monitor
lambda function to monitor AWS CodePipeline and annotate GitHub commits with status of pipeline action executions

## build and test

    bazel test //...

## updating dependencies

    bazel run syncdeps
