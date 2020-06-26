# pipeline-monitor

The project provides CodeBuild/CodePipeline integration with GitHub for better
developer experience.

1. Annotate GitHub commits with status of CodePipeline action executions - this is typically deployment pipelines
2. Post CodeBuild logs as PR comments (linters, tests, builds)

## build and test

    bazel test //...

## updating dependencies

    bazel run syncdeps
