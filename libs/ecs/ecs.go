package ecs

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/ngocson2vn/run-ecs-task/libs/cloudwatch"
	"github.com/ngocson2vn/run-ecs-task/libs/util"
)

type Task struct {
	Id                   string
	SourceTaskDefinition string
	TargetTaskDefinition string
	ClusterName          string
	ContainerName        string
	LaunchType           string
	Status               string
	Command              []string
}

const (
	TASK_STATUS_RUNNING string = "RUNNING"
	TASK_STATUS_STOPPED string = "STOPPED"
	RETRY_MAX           int    = 10
)

func GetTaskDefinition(taskName string) (*ecs.TaskDefinition, error) {
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

	retry := 0
	for err != nil && retry <= RETRY_MAX {
		time.Sleep(time.Duration(1) * time.Second)
		retry++

		taskDef, err = svcEcs.DescribeTaskDefinition(taskParams)
	}

	if err != nil {
		return nil, err
	}

	return taskDef.TaskDefinition, nil
}

func DescribeTask(ecsSvc *ecs.ECS, clusterName string, taskId string) (*ecs.Task, error) {
	input := &ecs.DescribeTasksInput{
		Cluster: aws.String(clusterName),
		Tasks: []*string{
			aws.String(taskId),
		},
	}

	retry := 0
	output, err := ecsSvc.DescribeTasks(input)
	for err != nil && retry <= RETRY_MAX {
		time.Sleep(time.Duration(1) * time.Second)
		retry++

		output, err = ecsSvc.DescribeTasks(input)
	}

	if err != nil {
		return nil, err
	}

	retry = 0
	for output != nil && len(output.Tasks) == 0 && retry <= RETRY_MAX {
		time.Sleep(time.Duration(1) * time.Second)
		retry++

		output, err = ecsSvc.DescribeTasks(input)
	}

	if err != nil {
		return nil, err
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
	log.Println(fmt.Sprintf("Successfully placed task: %s", task.Id))

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
		Task:    aws.String(task.Id),
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

func registerTargetTaskDefinition(targetTaskDef *ecs.TaskDefinition, sourceContainerDef *ecs.ContainerDefinition, containerName string) (*ecs.TaskDefinition, error) {
	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}
	ecsSvc := ecs.New(sess, &aws.Config{Region: aws.String(util.GetEnv("AWS_REGION", "ap-northeast-1"))})

	for _, container := range targetTaskDef.ContainerDefinitions {
		if *container.Name == containerName {
			container.Image = sourceContainerDef.Image
			container.Environment = sourceContainerDef.Environment
		}
	}

	newTaskDefInput := &ecs.RegisterTaskDefinitionInput{
		ContainerDefinitions:    targetTaskDef.ContainerDefinitions,
		Cpu:                     targetTaskDef.Cpu,
		ExecutionRoleArn:        targetTaskDef.ExecutionRoleArn,
		Family:                  targetTaskDef.Family,
		Memory:                  targetTaskDef.Memory,
		NetworkMode:             targetTaskDef.NetworkMode,
		PlacementConstraints:    targetTaskDef.PlacementConstraints,
		RequiresCompatibilities: targetTaskDef.RequiresCompatibilities,
		TaskRoleArn:             targetTaskDef.TaskRoleArn,
		Volumes:                 targetTaskDef.Volumes,
	}

	output, err := ecsSvc.RegisterTaskDefinition(newTaskDefInput)
	if err != nil {
		return nil, err
	}

	return output.TaskDefinition, nil
}

func containerDefinitionChanged(sourceContainerDef *ecs.ContainerDefinition, targetContainerDef *ecs.ContainerDefinition) bool {
	// Compare docker image name
	if *sourceContainerDef.Image != *targetContainerDef.Image {
		return true
	}

	// Compare environment variables
	sourceEnv := map[string]string{}
	targetEnv := map[string]string{}
	for _, env := range sourceContainerDef.Environment {
		sourceEnv[*env.Name] = *env.Value
	}

	for _, env := range targetContainerDef.Environment {
		targetEnv[*env.Name] = *env.Value
	}

	if len(sourceEnv) != len(targetEnv) {
		return true
	}

	for sk, sv := range sourceEnv {
		if tv, ok := targetEnv[sk]; ok {
			if tv != sv {
				return true
			}
		} else {
			return true
		}
	}

	return false
}

func updateTargetTaskDefinition(task *Task) error {
	sourceTaskDef, err := GetTaskDefinition(task.SourceTaskDefinition)
	if err != nil {
		return err
	}

	targetTaskDef, err := GetTaskDefinition(task.TargetTaskDefinition)
	if err != nil {
		return err
	}

	var sourceContainerDef *ecs.ContainerDefinition = nil
	var targetContainerDef *ecs.ContainerDefinition = nil
	for _, container := range sourceTaskDef.ContainerDefinitions {
		if *container.Name == task.ContainerName {
			sourceContainerDef = container
			break
		}
	}
	if sourceContainerDef == nil {
		return fmt.Errorf("Could not find the source container %s", task.ContainerName)
	}

	for _, container := range targetTaskDef.ContainerDefinitions {
		if *container.Name == task.ContainerName {
			targetContainerDef = container
			break
		}
	}
	if targetContainerDef == nil {
		return fmt.Errorf("Could not find the target container %s", task.ContainerName)
	}

	if containerDefinitionChanged(sourceContainerDef, targetContainerDef) {
		latestTargetTaskDef, err := registerTargetTaskDefinition(targetTaskDef, sourceContainerDef, task.ContainerName)
		if err != nil {
			return err
		}
		log.Println(fmt.Sprintf("Latest task definition: %s", *latestTargetTaskDef.TaskDefinitionArn))
	}

	return nil
}

func placeTask(ecsSvc *ecs.ECS, task *Task, desiredCount int64) ([]string, error) {
	err := updateTargetTaskDefinition(task)
	if err != nil {
		return []string{}, err
	}

	cmd := []*string{}
	for _, c := range task.Command {
		cmd = append(cmd, aws.String(c))
	}

	containerOverride := &ecs.ContainerOverride{
		Command: cmd,
		Name:    aws.String(task.ContainerName),
	}

	taskOverride := &ecs.TaskOverride{
		ContainerOverrides: []*ecs.ContainerOverride{containerOverride},
	}

	runTaskInput := &ecs.RunTaskInput{
		Cluster:        aws.String(task.ClusterName),
		Count:          aws.Int64(desiredCount),
		LaunchType:     aws.String(task.LaunchType),
		Overrides:      taskOverride,
		TaskDefinition: aws.String(task.TargetTaskDefinition),
	}

	retry := 0
	output, err := ecsSvc.RunTask(runTaskInput)

	for (err != nil || len(output.Tasks) == 0) && retry <= RETRY_MAX {
		time.Sleep(time.Duration(1) * time.Second)
		retry++

		output, err = ecsSvc.RunTask(runTaskInput)
	}

	if err != nil {
		return []string{}, err
	}

	taskIDs := []string{}
	for _, placedTask := range output.Tasks {
		taskIDs = append(taskIDs, strings.Split(*placedTask.TaskArn, "/")[1])
	}

	return taskIDs, nil
}

func traceTask(ecsSvc *ecs.ECS, taskId string, task *Task) error {
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
	log.Println(fmt.Sprintf("LogGroupName: %s", logGroupName))
	log.Println(fmt.Sprintf("LogStreamName: %s", logStreamName))

	prevTokenValue := ""
	nextTokenValue := ""
	isFirstRound := true

	//=======================================
	// Task Status: RUNNING
	//=======================================
	if *ecsTask.LastStatus == TASK_STATUS_RUNNING {
		log.Println(fmt.Sprintf("Task Status: %s", *ecsTask.LastStatus))

		for *ecsTask.LastStatus == TASK_STATUS_RUNNING {
			messages, nextToken := cloudwatch.GetLogEvents(logGroupName, logStreamName, nextTokenValue)
			for _, m := range messages {
				log.Println(*m)
			}

			if nextToken != nil {
				nextTokenValue = *nextToken

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
	if len(nextTokenValue) == 0 {
		messages, nextToken := cloudwatch.GetLogEvents(logGroupName, logStreamName, nextTokenValue)
		for _, m := range messages {
			log.Println(*m)
		}

		if nextToken != nil {
			nextTokenValue = *nextToken
		}
	}

	prevTokenValue = ""

	for prevTokenValue != nextTokenValue {
		prevTokenValue = nextTokenValue

		messages, nextToken := cloudwatch.GetLogEvents(logGroupName, logStreamName, nextTokenValue)
		for _, m := range messages {
			log.Println(*m)
		}

		if nextToken != nil {
			nextTokenValue = *nextToken
		}
	}

	log.Println(fmt.Sprintf("Task Status: %s", *ecsTask.LastStatus))
	region := util.GetEnv("AWS_REGION", "ap-northeast-1")
	log.Println(fmt.Sprintf("CloudWatchLogs: https://%s.console.aws.amazon.com/cloudwatch/home?region=%s#logEventViewer:group=%s;stream=%s", region, region, logGroupName, logStreamName))

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
