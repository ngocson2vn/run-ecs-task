package main

import (
	"fmt"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ngocson2vn/run-ecs-task/libs/ecs"
)

type RequiredEnv struct {
	SourceTaskDefinition           string
	TargetTaskDefinition           string
	ClusterName                    string
	ContainerName                  string
	LaunchType                     string
}

func handleError(err error) {
	if err != nil {
		log.Fatalln(err.Error())
	}
}

func getRequiredEnv() (RequiredEnv, error) {
	sourceTaskDefinition := os.Getenv("SOURCE_TASK_DEFINITION")
	targetTaskDefinition := os.Getenv("TARGET_TASK_DEFINITION")
	clusterName          := os.Getenv("CLUSTER_NAME")
	containerName        := os.Getenv("CONTAINER_NAME")
	launchType           := os.Getenv("LAUNCH_TYPE")

	errMessages := []string{}
	if len(sourceTaskDefinition) == 0 {
		errMessages = append(errMessages, "SOURCE_TASK_DEFINITION is empty. Please set a value for it.")
	}

	if len(targetTaskDefinition) == 0 {
		errMessages = append(errMessages, "TARGET_TASK_DEFINITION is empty. Please set a value for it.")
	}

	if len(clusterName) == 0 {
		errMessages = append(errMessages, "CLUSTER_NAME is empty. Please set a value for it.")
	}

	if len(containerName) == 0 {
		errMessages = append(errMessages, "CONTAINER_NAME is empty. Please set a value for it.")
	}

	if len(launchType) == 0 {
		launchType = "EC2"
	}

	if len(errMessages) > 0 {
		return RequiredEnv{}, fmt.Errorf("%s", strings.Join(errMessages[:], "\n"))
	}

	requiredEnv := RequiredEnv{
		SourceTaskDefinition: sourceTaskDefinition,
		TargetTaskDefinition: targetTaskDefinition,
		ClusterName:          clusterName,
		ContainerName:        containerName,
		LaunchType:           launchType,
	}

	return requiredEnv, nil
}


func main() {
	log.Println("Version 0.0.12")
	sigs := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	env, err := getRequiredEnv()
	handleError(err)

	command := flag.String("command", "", "Command to be executed")
	flag.Parse()
	if command == nil || len(*command) == 0 {
		handleError(fmt.Errorf("Command is empty. Please specify a command with --command option."))
	}

	task := &ecs.Task {
		SourceTaskDefinition:  env.SourceTaskDefinition,
		TargetTaskDefinition:  env.TargetTaskDefinition,
		ClusterName:           env.ClusterName,
		ContainerName:         env.ContainerName,
		LaunchType:            env.LaunchType,
		Command:               []string{"sh", "-l", "-c", *command},
	}

	// Handle cancellation
	go func(task *ecs.Task) {
		<-sigs

		if len(task.Id) == 0 {
			time.Sleep(time.Duration(30) * time.Second)
		}

		if task.Status != ecs.TASK_STATUS_STOPPED {
			err := ecs.StopTask(task)
			handleError(err)
			log.Println(fmt.Sprintf("Sent SIGTERM to task %s", task.Id))
		}

		done <- true
	}(task)


	err = ecs.RunTask(task)
	handleError(err)

	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	<-done
}
