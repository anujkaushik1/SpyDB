package dynamo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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
	if strings.HasPrefix(profile, keysPrefix) {
		return s.connectWithKeys(profile)
	}
	profiles, err := awsProfiles()
	if err != nil {
		return Connection{}, err
	}
	i := slices.IndexFunc(profiles, func(p AWSProfile) bool { return p.Name == profile })
	if i < 0 {
		return Connection{}, fmt.Errorf("There's no AWS profile named %q in %s.", profile, awsFolder)
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
	s.client, s.tables = client, nil
	s.mu.Unlock()
	return Connection{Profile: profile, Region: region}, nil
}

// connectWithKeys is Connect for keys typed in (KeySignIn): the library
// gets just those keys, nothing from ~/.aws.
func (s *Service) connectWithKeys(profile string) (Connection, error) {
	s.mu.Lock()
	k := s.keys
	s.mu.Unlock()
	if k == nil || keysPrefix+k.Name != profile {
		return Connection{}, errors.New("Sign in with the keys first.")
	}
	cfg, err := s.keysConfig(context.Background(), *k)
	if err != nil {
		return Connection{}, err
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if s.endpoint != "" {
			o.BaseEndpoint = aws.String(s.endpoint)
		}
	})
	s.mu.Lock()
	s.client, s.tables = client, nil
	s.mu.Unlock()
	return Connection{Profile: profile, Region: k.Region}, nil
}

// Disconnect stops showing DynamoDB; you're back on the sign-in screen.
// Keys signed in with are forgotten (saved ones stay in their file).
func (s *Service) Disconnect() {
	s.mu.Lock()
	s.client, s.tables, s.keys = nil, nil, nil
	s.mu.Unlock()
}

// ListTables lists the tables in the connected region by name, in AWS's
// (alphabetical) order. AWS gives up to 100 names per call; this asks
// until it has them all.
func (s *Service) ListTables(ctx context.Context) ([]string, error) {
	client, err := s.dynamoClient()
	if err != nil {
		return nil, err
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

// TableSummary is a table as the sidebar lists it: its name and about how
// many items it holds (AWS's count, updated about every 6 hours).
type TableSummary struct {
	Name      string `json:"name"`
	ItemCount *int64 `json:"itemCount"` // nil when AWS wouldn't describe the table (a profile allowed to list but not describe)
	SizeBytes *int64 `json:"sizeBytes"`
}

// ListTableSummaries is ListTables with each table's item count: one
// DescribeTable per table, eight at a time (DescribeTable is free and
// reads no items). A table that can't be described is listed without a count.
func (s *Service) ListTableSummaries(ctx context.Context) ([]TableSummary, error) {
	names, err := s.ListTables(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TableSummary, len(names))
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i, name := range names {
		out[i].Name = name
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if d, err := s.DescribeTable(ctx, name); err == nil {
				count, size := d.ItemCount, d.SizeBytes
				out[i].ItemCount, out[i].SizeBytes = &count, &size
			}
		}(i, name)
	}
	wg.Wait()
	return out, nil
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
