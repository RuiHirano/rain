package cfn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	smithy "github.com/awslabs/smithy-go"

	"github.com/aws-cloudformation/rain/cfn"
	"github.com/aws-cloudformation/rain/cfn/format"
	"github.com/aws-cloudformation/rain/client"
	"github.com/aws-cloudformation/rain/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

var liveStatuses = []types.StackStatus{
	"CREATE_IN_PROGRESS",
	"CREATE_FAILED",
	"CREATE_COMPLETE",
	"ROLLBACK_IN_PROGRESS",
	"ROLLBACK_FAILED",
	"ROLLBACK_COMPLETE",
	"DELETE_IN_PROGRESS",
	"DELETE_FAILED",
	"UPDATE_IN_PROGRESS",
	"UPDATE_COMPLETE_CLEANUP_IN_PROGRESS",
	"UPDATE_COMPLETE",
	"UPDATE_ROLLBACK_IN_PROGRESS",
	"UPDATE_ROLLBACK_FAILED",
	"UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS",
	"UPDATE_ROLLBACK_COMPLETE",
	"REVIEW_IN_PROGRESS",
}

func getClient() *cloudformation.Client {
	return cloudformation.NewFromConfig(client.Config())
}

// GetStackTemplate returns the template used to launch the named stack
func GetStackTemplate(stackName string, processed bool) (string, error) {
	templateStage := "Original"
	if processed {
		templateStage = "Processed"
	}

	res, err := getClient().GetTemplate(context.Background(), &cloudformation.GetTemplateInput{
		StackName:     &stackName,
		TemplateStage: types.TemplateStage(templateStage),
	})
	if err != nil {
		return "", err
	}

	return *res.TemplateBody, nil
}

// StackExists checks whether the named stack currently exists
func StackExists(stackName string) (bool, error) {
	stacks, err := ListStacks()
	if err != nil {
		return false, err
	}

	for _, s := range stacks {
		if *s.StackName == stackName {
			return true, nil
		}
	}

	return false, nil
}

// ListStacks returns a list of all existing stacks
func ListStacks() ([]*types.StackSummary, error) {
	stacks := make([]*types.StackSummary, 0)

	var token *string

	for {
		res, err := getClient().ListStacks(context.Background(), &cloudformation.ListStacksInput{
			NextToken:         token,
			StackStatusFilter: liveStatuses,
		})

		if err != nil {
			return stacks, err
		}

		stacks = append(stacks, res.StackSummaries...)

		if res.NextToken == nil {
			break
		}

		token = res.NextToken
	}

	return stacks, nil
}

// DeleteStack deletes a stack
func DeleteStack(stackName string) error {
	// Get the stack properties
	_, err := getClient().DeleteStack(context.Background(), &cloudformation.DeleteStackInput{
		StackName: &stackName,
	})

	return err
}

// SetTerminationProtection enables or disables termination protection for a stack
func SetTerminationProtection(stackName string, protectionEnabled bool) error {
	// Set termination protection
	_, err := getClient().UpdateTerminationProtection(context.Background(), &cloudformation.UpdateTerminationProtectionInput{
		StackName:                   &stackName,
		EnableTerminationProtection: aws.Bool(protectionEnabled),
	})

	return err
}

// GetStack returns a cloudformation.Stack representing the named stack
func GetStack(stackName string) (*types.Stack, error) {
	// Get the stack properties
	res, err := getClient().DescribeStacks(context.Background(), &cloudformation.DescribeStacksInput{
		StackName: &stackName,
	})
	if err != nil {
		return &types.Stack{}, err
	}

	return res.Stacks[0], nil
}

// GetStackResources returns a list of the resources in the named stack
func GetStackResources(stackName string) ([]*types.StackResource, error) {
	// Get the stack resources
	res, err := getClient().DescribeStackResources(context.Background(), &cloudformation.DescribeStackResourcesInput{
		StackName: &stackName,
	})
	if err != nil {
		return nil, err
	}

	return res.StackResources, nil
}

// GetStackEvents returns all events associated with the named stack
func GetStackEvents(stackName string) ([]*types.StackEvent, error) {
	events := make([]*types.StackEvent, 0)

	var token *string

	for {
		res, err := getClient().DescribeStackEvents(context.Background(), &cloudformation.DescribeStackEventsInput{
			NextToken: token,
			StackName: &stackName,
		})

		if err != nil {
			return events, err
		}

		events = append(events, res.StackEvents...)

		if res.NextToken == nil {
			break
		}

		token = res.NextToken
	}

	return events, nil
}

func makeTags(tags map[string]string) []*types.Tag {
	out := make([]*types.Tag, 0)

	for key, value := range tags {
		out = append(out, &types.Tag{
			Key:   aws.String(key),
			Value: aws.String(value),
		})
	}

	return out
}

// CreateChangeSet creates a changeset
func CreateChangeSet(template cfn.Template, params []*types.Parameter, tags map[string]string, stackName string) (string, error) {
	templateBody := format.Template(template, format.Options{})

	changeSetType := "CREATE"

	exists, err := StackExists(stackName)
	if err != nil {
		return "", err
	}

	if exists {
		changeSetType = "UPDATE"
	}

	changeSetName := stackName + "-" + fmt.Sprint(time.Now().Unix())

	_, err = getClient().CreateChangeSet(context.Background(), &cloudformation.CreateChangeSetInput{
		ChangeSetType: types.ChangeSetType(changeSetType),
		ChangeSetName: &changeSetName,
		StackName:     &stackName,
		TemplateBody:  &templateBody,
		Tags:          makeTags(tags),
		Parameters:    params,
		Capabilities: []types.Capability{
			"CAPABILITY_NAMED_IAM",
			"CAPABILITY_AUTO_EXPAND",
		},
	})
	if err != nil {
		return changeSetName, err
	}

	for {
		res, err := getClient().DescribeChangeSet(context.Background(), &cloudformation.DescribeChangeSetInput{
			ChangeSetName: &changeSetName,
			StackName:     &stackName,
		})
		if err != nil {
			return changeSetName, err
		}

		status := string(res.Status)
		config.Debugf("ChangeSet status: %s", status)

		if status == "FAILED" {
			return changeSetName, errors.New(aws.ToString(res.StatusReason))
		}

		if strings.HasSuffix(status, "_COMPLETE") {
			break
		}

		time.Sleep(time.Second * 2)
	}

	return changeSetName, nil
}

// GetChangeSet returns the named changeset
func GetChangeSet(stackName, changeSetName string) (*cloudformation.DescribeChangeSetOutput, error) {
	return getClient().DescribeChangeSet(context.Background(), &cloudformation.DescribeChangeSetInput{
		ChangeSetName: &changeSetName,
		StackName:     &stackName,
	})
}

// ExecuteChangeSet executes the named changeset
func ExecuteChangeSet(stackName, changeSetName string) error {
	_, err := getClient().ExecuteChangeSet(context.Background(), &cloudformation.ExecuteChangeSetInput{
		ChangeSetName: &changeSetName,
		StackName:     &stackName,
	})

	return err
}

// DeleteChangeSet deletes the named changeset
func DeleteChangeSet(stackName, changeSetName string) error {
	_, err := getClient().DeleteChangeSet(context.Background(), &cloudformation.DeleteChangeSetInput{
		ChangeSetName: &changeSetName,
		StackName:     &stackName,
	})

	return err
}

// WaitUntilStackExists pauses execution until the named stack exists
func WaitUntilStackExists(stackName string) error {
	for {
		_, err := getClient().DescribeStacks(context.Background(), &cloudformation.DescribeStacksInput{
			StackName: aws.String(stackName),
		})

		if err == nil {
			break
		}

		var apiErr = &smithy.GenericAPIError{}
		if !errors.As(err, &apiErr) {
			return err
		}

		time.Sleep(time.Second * 2)
	}

	return nil
}

// WaitUntilStackCreateComplete pauses execution until the stack is completed (or fails)
func WaitUntilStackCreateComplete(stackName string) error {
	for {
		res, err := getClient().DescribeStacks(context.Background(), &cloudformation.DescribeStacksInput{
			StackName: aws.String(stackName),
		})

		if err != nil {
			return err
		}

		if len(res.Stacks) != 1 {
			return errors.New("Stack not found")
		}

		stack := *res.Stacks[0]

		status := string(stack.StackStatus)
		if strings.HasSuffix(status, "_COMPLETE") || strings.HasSuffix(status, "_FAILED") {
			break
		}

		time.Sleep(time.Second * 2)
	}

	return nil
}
