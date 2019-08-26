package ecs

import (
	"fmt"
	"time"
	"strings"

	"go.uber.org/zap"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/ngocson2vn/run-ecs-task/libs/util"
	"github.com/ngocson2vn/run-ecs-task/libs/cloudwatch"
)

type Task struct {
	Id                    string
	SourceTaskDefinition  string
	TargetTaskDefinition  string
	ClusterName           string
	ContainerName         string
	LaunchType            string
	Status                string
	Command               []string
	Logger                *zap.Logger
}

const (
	TASK_STATUS_RUNNING string = "RUNNING"
	TASK_STATUS_STOPPED string = "STOPPED"
	RETRY_MAX int = 3
)

func GetContainerDefinition(taskName string, containerName string) (*ecs.ContainerDefinition, error) {
	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}
	svcEcs := ecs.New(sess, &aws.Config{Region: aws.String(util.GetEnv("AWS_REGION", "ap-northeast-1"))})

	// Get Current TaskDefinition
	taskParams := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskName),
	}
	taskDef, err := svcEcs.DescribeTaskDefinition(taskParams)
	if err != nil {
		return nil, err
	}

	for _, container := range taskDef.TaskDefinition.ContainerDefinitions {
		if *container.Name == containerName {
			return container, nil
		}
	}

	return nil, fmt.Errorf("Could not find container %s in task definition %s", containerName, taskName)
}


func DescribeTask(ecsSvc *ecs.ECS, clusterName string, taskId string) (*ecs.Task, error) {
	input := &ecs.DescribeTasksInput{
		Cluster: aws.String(clusterName),
		Tasks: []*string{
			aws.String(taskId),
		},
	}

	output, err := ecsSvc.DescribeTasks(input)
	if err != nil {
		return nil, err
	}

	retry := 0
	for output != nil && len(output.Tasks) == 0 && retry <= RETRY_MAX {
		time.Sleep(time.Duration(1) * time.Second)
		retry++

		output, err = ecsSvc.DescribeTasks(input)
		if err != nil {
			return nil, err
		}
	}

	if len(output.Tasks) > 0 {
		return output.Tasks[0], nil
	}

	return nil, fmt.Errorf("Could not find the task: %s", taskId)
}


func RunTask(task *Task) error {
	sess, err := session.NewSession()
	if err != nil {
		return err
	}
	ecsSvc := ecs.New(sess, &aws.Config{Region: aws.String(util.GetEnv("AWS_REGION", "ap-northeast-1"))})

	taskIDs, err := placeTask(ecsSvc, task, 1)
	if err != nil {
		return err
	}

	task.Id = taskIDs[0]
	task.Logger.Info(fmt.Sprintf("Successfully placed task: %s", task.Id))

	time.Sleep(time.Duration(3) * time.Second)
	err = traceTask(ecsSvc, taskIDs[0], task)
	if err != nil {
		return err
	}

	return nil
}


func StopTask(task *Task) error {
	sess, err := session.NewSession()
	if err != nil {
		return err
	}
	ecsSvc := ecs.New(sess, &aws.Config{Region: aws.String(util.GetEnv("AWS_REGION", "ap-northeast-1"))})

	input := &ecs.StopTaskInput{
		Cluster: aws.String(task.ClusterName),
		Task: aws.String(task.Id),
	}

	retry := 0
	_, err = ecsSvc.StopTask(input)

	for err != nil && retry <= RETRY_MAX {
		time.Sleep(time.Duration(1) * time.Second)
		retry++
		_, err = ecsSvc.StopTask(input)
	}

	if err != nil {
		return err
	}

	return nil
}


func placeTask(ecsSvc *ecs.ECS, task *Task, desiredCount int64) ([]string, error) {
	sourceContainerDef, err := GetContainerDefinition(task.SourceTaskDefinition, task.ContainerName)
	if err != nil {
		return []string{}, err
	}

	cmd := []*string{}
	for _, c := range task.Command {
		cmd = append(cmd, aws.String(c))
	}

	containerOverride := &ecs.ContainerOverride{
		Command: cmd,
		Environment: sourceContainerDef.Environment,
		Name: sourceContainerDef.Name,
	}

	taskOverride := &ecs.TaskOverride{
		ContainerOverrides: []*ecs.ContainerOverride{containerOverride},
	}

	runTaskInput := &ecs.RunTaskInput{
		Cluster: aws.String(task.ClusterName),
		Count: aws.Int64(desiredCount),
		LaunchType: aws.String(task.LaunchType),
		Overrides: taskOverride,
		TaskDefinition: aws.String(task.TargetTaskDefinition),
	}

	output, err := ecsSvc.RunTask(runTaskInput)
	if err != nil {
		errMsg := err.Error()
		for _, failure := range output.Failures {
			errMsg = fmt.Sprintf("%s\n%s\n%s", errMsg, failure.Arn, failure.Reason)
		}

		return []string{}, fmt.Errorf(errMsg)
	}

	taskIDs := []string{}
	for _, placedTask := range output.Tasks {
		taskIDs = append(taskIDs, strings.Split(*placedTask.TaskArn, "/")[1])
	}

	return taskIDs, nil
}


func traceTask(ecsSvc *ecs.ECS, taskId string, task *Task) error {
	logger := task.Logger

	ecsTask, err := DescribeTask(ecsSvc, task.ClusterName, taskId)
	if err != nil {
		return err
	}

	// Update task status
	task.Status = *ecsTask.LastStatus

	// Wait until task status becomes RUNNING or STOPPED
	for *ecsTask.LastStatus != TASK_STATUS_RUNNING && *ecsTask.LastStatus != TASK_STATUS_STOPPED {
		time.Sleep(time.Duration(1) * time.Second)

		ecsTask, err = DescribeTask(ecsSvc, task.ClusterName, taskId)
		if err != nil {
			return err
		}

		// Update task status
		task.Status = *ecsTask.LastStatus
	}


	// Fetch logs from CloudWatch Logs
	logGroupName := fmt.Sprintf("/ecs/%s", task.TargetTaskDefinition)
	logStreamName := fmt.Sprintf("ecs/%s/%s", task.ContainerName, taskId)
	logger.Info(fmt.Sprintf("LogGroupName: %s", logGroupName))
	logger.Info(fmt.Sprintf("LogStreamName: %s", logStreamName))

	prevTokenValue := ""
	nextTokenValue := ""
	isFirstRound := true


	//=======================================
	// Task Status: RUNNING
	//=======================================
	if *ecsTask.LastStatus == TASK_STATUS_RUNNING {
		logger.Info(fmt.Sprintf("Task Status: %s", *ecsTask.LastStatus))

		for *ecsTask.LastStatus == TASK_STATUS_RUNNING {
			messages, nextToken := cloudwatch.GetLogEvents(logGroupName, logStreamName, nextTokenValue)
			for _, m := range messages {
				logger.Info(*m)
			}

			if nextToken != nil {
				nextTokenValue = *nextToken

				// if len(messages) > 0 {
				// 	logger.Info(fmt.Sprintf("==> Next Token: %s", nextTokenValue))
				// }

				if nextTokenValue == prevTokenValue {
					nextTokenValue = ""
				}
			}

			if isFirstRound && len(nextTokenValue) > 0 {
				prevTokenValue = nextTokenValue
				isFirstRound = false
			}

			time.Sleep(time.Duration(3) * time.Second)
			ecsTask, err = DescribeTask(ecsSvc, task.ClusterName, taskId)
			if err != nil {
				return err
			}

			// Update task status
			task.Status = *ecsTask.LastStatus
		}
	}



	//=======================================
	// Task Status: STOPPED
	//=======================================
	prevTokenValue = ""
	nextTokenValue = ""
	messages, nextToken := cloudwatch.GetLogEvents(logGroupName, logStreamName, nextTokenValue)
	if nextToken != nil {
		nextTokenValue = *nextToken
	}

	for prevTokenValue != nextTokenValue {
		for _, m := range messages {
			logger.Info(*m)
		}

		prevTokenValue = nextTokenValue

		messages, nextToken = cloudwatch.GetLogEvents(logGroupName, logStreamName, nextTokenValue)
		if nextToken != nil {
			nextTokenValue = *nextToken
		}
	}

	logger.Info(fmt.Sprintf("Task Status: %s", *ecsTask.LastStatus))

	// Get task container's ExitCode
	for _, c := range ecsTask.Containers {
		if *c.Name == task.ContainerName {
			if c.ExitCode != nil && *c.ExitCode != 0 {
				return fmt.Errorf("Task failed with ExitCode = %d", *c.ExitCode)
			}

			break
		}
	}

	return nil
}
