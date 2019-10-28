package cloudwatch

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
)

func GetLogEvents(logGroupName string, logStreamName string, nextToken string) ([]*string, *string) {
	messages := []*string{}

	sess, err := session.NewSession()
	if err != nil {
		return messages, nil
	}
	svc := cloudwatchlogs.New(sess, &aws.Config{Region: aws.String("ap-northeast-1")})

	var token *string = nil
	if len(nextToken) > 0 {
		token = aws.String(nextToken)
	}

	input := &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  aws.String(logGroupName),
		LogStreamName: aws.String(logStreamName),
		StartFromHead: aws.Bool(true),
		NextToken:     token,
	}

	output, err := svc.GetLogEvents(input)
	if err != nil {
		return messages, nil
	}

	for _, event := range output.Events {
		messages = append(messages, event.Message)
	}

	return messages, output.NextForwardToken
}