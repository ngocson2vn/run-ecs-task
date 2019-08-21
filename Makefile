macos:
	env GOOS=darwin go build -ldflags="-s -w" -o bin/run-ecs-task scripts/run-ecs-task.go

linux:
	env GOOS=linux go build -ldflags="-s -w" -o bin/run-ecs-task scripts/run-ecs-task.go	
