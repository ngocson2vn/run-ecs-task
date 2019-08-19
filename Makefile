macos:
	env GOOS=darwin go build -ldflags="-s -w" -o bin/ecs-task-executor scripts/ecs-task-executor.go

linux:
	env GOOS=linux go build -ldflags="-s -w" -o bin/ecs-task-executor scripts/ecs-task-executor.go	
