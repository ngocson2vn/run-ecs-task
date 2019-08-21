# run-ecs-task
A tool for running an ECS Task and outputting its logs.

## Architecture Diagram
![image](https://user-images.githubusercontent.com/1695690/63397153-9e85e880-c404-11e9-8c89-c9bdf704c881.png)

## Build
### MacOS
```bash
make macos
```

### Linux
```bash
make linux
```

## Usage
```bash
# ECS cluster name
export CLUSTER_NAME=SampleCluster

# Container name
export CONTAINER_NAME=sample_app

# Task A
export SOURCE_TASK_DEFINITION=SampleApp

# Task A1
export TARGET_TASK_DEFINITION=SampleApp_batch

run-ecs-task --command "command to be executed"
```