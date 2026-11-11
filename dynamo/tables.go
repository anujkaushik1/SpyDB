package dynamo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/smithy-go"
)

// Connection is the AWS profile and region whose DynamoDB tables SpyDB shows.
type Connection struct {
	Profile string `json:"profile"`
	Region  string `json:"region"` // e.g. "ap-south-1": every DynamoDB table lives in one region
}

// defaultRegion is for a profile with no region of its own, as in signIn.
const defaultRegion = "us-east-1"

// Connect starts showing DynamoDB with a profile you've signed in with, in
// that profile's region. From here SpyDB talks to DynamoDB with Go's AWS
// library, not the AWS CLI: the library reads the same profile from ~/.aws,
// with the keys or the sign-in the CLI left there, so SpyDB copies and
// stores nothing. Nothing goes to AWS yet; ListTables is the first call.
func (s *Service) Connect(ctx context.Context, profile string) (Connection, error) {
	profiles, err := awsProfiles()
	if err != nil {
		return Connection{}, err
	}
	i := slices.IndexFunc(profiles, func(p AWSProfile) bool { return p.Name == profile })
	if i < 0 {
		return Connection{}, fmt.Errorf("There's no AWS profile named %q in ~/.aws.", profile)
	}
	region := profiles[i].Region
	if region == "" {
		region = defaultRegion
	}
	// The profile named here wins over any AWS keys in SpyDB's environment.
	cfg, err := config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile(profile), config.WithRegion(region))
	if err != nil {
		return Connection{}, fmt.Errorf("Couldn't read the AWS profile %q: %w", profile, err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if s.endpoint != "" {
			o.BaseEndpoint = aws.String(s.endpoint)
		}
	})
	s.mu.Lock()
	s.client = client
	s.mu.Unlock()
	return Connection{Profile: profile, Region: region}, nil
}

// Disconnect stops showing DynamoDB; you're back on the sign-in screen.
func (s *Service) Disconnect() {
	s.mu.Lock()
	s.client = nil
	s.mu.Unlock()
}

// ListTables lists the tables in the connected region by name, in AWS's
// (alphabetical) order. AWS gives up to 100 names per call; this asks
// until it has them all.
func (s *Service) ListTables(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client == nil {
		return nil, errors.New("Not connected to DynamoDB: sign in first.")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	names := []string{}
	pages := dynamodb.NewListTablesPaginator(client, &dynamodb.ListTablesInput{})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, awsError(err)
		}
		names = append(names, page.TableNames...)
	}
	return names, nil
}

// awsError is AWS's own message for a refused call, without the library's
// request details, saying what to do for the common ones.
func awsError(err error) error {
	var api smithy.APIError
	if !errors.As(err, &api) {
		return err // couldn't reach AWS, or get credentials (an expired sign-in)
	}
	switch api.ErrorCode() {
	case "AccessDeniedException":
		return fmt.Errorf("AWS says this profile isn't allowed to do that: %s", api.ErrorMessage())
	case "UnrecognizedClientException", "InvalidSignatureException", "ExpiredTokenException":
		return fmt.Errorf("AWS didn't accept this profile's credentials (%s). Disconnect and sign in again.", api.ErrorMessage())
	}
	return fmt.Errorf("AWS: %s", api.ErrorMessage())
}
