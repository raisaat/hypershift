package aws

import (
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/openshift/hypershift/cmd/log"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

const duplicatePermissionErrorCode = "InvalidPermission.Duplicate"

func (o *CreateInfraOptions) CreateWorkerSecurityGroup(client ec2iface.EC2API, vpcID string) (string, error) {
	groupName := fmt.Sprintf("%s-worker-sg", o.InfraID)
	securityGroup, err := o.existingSecurityGroup(client, groupName)
	if err != nil {
		return "", err
	}
	if securityGroup == nil {
		result, err := client.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
			GroupName:         aws.String(groupName),
			Description:       aws.String("worker security group"),
			VpcId:             aws.String(vpcID),
			TagSpecifications: o.ec2TagSpecifications("security-group", groupName),
		})
		if err != nil {
			return "", fmt.Errorf("cannot create worker security group: %w", err)
		}
		backoff := wait.Backoff{
			Steps:    10,
			Duration: 3 * time.Second,
			Factor:   1.0,
			Jitter:   0.1,
		}
		var sgResult *ec2.DescribeSecurityGroupsOutput
		err = retry.OnError(backoff, func(error) bool { return true }, func() error {
			var err error
			sgResult, err = client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
				GroupIds: []*string{result.GroupId},
			})
			if err != nil || len(sgResult.SecurityGroups) == 0 {
				return fmt.Errorf("not found yet")
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("cannot find security group that was just created (%s)", aws.StringValue(result.GroupId))
		}
		securityGroup = sgResult.SecurityGroups[0]
		log.Log.Info("Created security group", "name", groupName, "id", aws.StringValue(securityGroup.GroupId))
	} else {
		log.Log.Info("Found existing security group", "name", groupName, "id", aws.StringValue(securityGroup.GroupId))
	}
	securityGroupID := aws.StringValue(securityGroup.GroupId)
	sgUserID := aws.StringValue(securityGroup.OwnerId)
	egressPermissions := []*ec2.IpPermission{
		{
			IpProtocol: aws.String("-1"),
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String("0.0.0.0/0"),
				},
			},
		},
	}
	ingressPermissions := []*ec2.IpPermission{
		{
			IpProtocol: aws.String("icmp"),
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String(DefaultCIDRBlock),
				},
			},
			FromPort: aws.Int64(-1),
			ToPort:   aws.Int64(-1),
		},
		{
			IpProtocol: aws.String("tcp"),
			IpRanges: []*ec2.IpRange{
				{
					CidrIp: aws.String(DefaultCIDRBlock),
				},
			},
			FromPort: aws.Int64(22),
			ToPort:   aws.Int64(22),
		},
		{
			FromPort:   aws.Int64(4789),
			ToPort:     aws.Int64(4789),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(6081),
			ToPort:     aws.Int64(6081),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(500),
			ToPort:     aws.Int64(500),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(4500),
			ToPort:     aws.Int64(4500),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			IpProtocol: aws.String("50"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(9000),
			ToPort:     aws.Int64(9999),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(9000),
			ToPort:     aws.Int64(9999),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(10250),
			ToPort:     aws.Int64(10250),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(30000),
			ToPort:     aws.Int64(32767),
			IpProtocol: aws.String("tcp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
		{
			FromPort:   aws.Int64(30000),
			ToPort:     aws.Int64(32767),
			IpProtocol: aws.String("udp"),
			UserIdGroupPairs: []*ec2.UserIdGroupPair{
				{
					GroupId: aws.String(securityGroupID),
					UserId:  aws.String(sgUserID),
				},
			},
		},
	}

	var egressToAuthorize []*ec2.IpPermission
	var ingressToAuthorize []*ec2.IpPermission

	for _, permission := range egressPermissions {
		if !includesPermission(securityGroup.IpPermissionsEgress, permission) {
			egressToAuthorize = append(egressToAuthorize, permission)
		}
	}

	for _, permission := range ingressPermissions {
		if !includesPermission(securityGroup.IpPermissions, permission) {
			ingressToAuthorize = append(ingressToAuthorize, permission)
		}
	}

	if len(egressToAuthorize) > 0 {
		_, err = client.AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{
			GroupId:       aws.String(securityGroupID),
			IpPermissions: egressToAuthorize,
		})
		var awsErr awserr.Error
		if err != nil {
			if errors.As(err, &awsErr) {
				// only return an error if the permission has not already been set
				if awsErr.Code() != duplicatePermissionErrorCode {
					return "", fmt.Errorf("cannot apply security group egress permissions: %w", err)
				}
			}
		}
		log.Log.Info("Authorized egress rules on security group", "id", securityGroupID)
	}
	if len(ingressToAuthorize) > 0 {
		_, err = client.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:       aws.String(securityGroupID),
			IpPermissions: ingressToAuthorize,
		})
		var awsErr awserr.Error
		if err != nil {
			if errors.As(err, &awsErr) {
				// only return an error if the permission has not already been set
				if awsErr.Code() != duplicatePermissionErrorCode {
					return "", fmt.Errorf("cannot apply security group ingress permissions: %w", err)
				}
			}
		}
		log.Log.Info("Authorized ingress rules on security group", "id", securityGroupID)
	}
	return securityGroupID, nil
}

func (o *CreateInfraOptions) existingSecurityGroup(client ec2iface.EC2API, name string) (*ec2.SecurityGroup, error) {
	result, err := client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{Filters: o.ec2Filters(name)})
	if err != nil {
		return nil, fmt.Errorf("cannot list security groups: %w", err)
	}
	for _, sg := range result.SecurityGroups {
		return sg, nil
	}
	return nil, nil
}

func includesPermission(list []*ec2.IpPermission, permission *ec2.IpPermission) bool {
	for _, p := range list {
		if samePermission(p, permission) {
			return true
		}
	}
	return false
}

func samePermission(a, b *ec2.IpPermission) bool {
	if a == nil || b == nil {
		return false
	}
	if a.String() == b.String() {
		return true
	}
	return false
}
