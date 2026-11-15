package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/anujkaushik1/SpyDB/appdata"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Signing in with an access key and secret key typed in, instead of an AWS
// profile: for anyone with keys but no AWS CLI or ~/.aws. The keys can be
// saved, and then they're kept in aws-keys.json in SpyDB's settings folder,
// readable only by your user, like Postgres's saved connections. Nothing
// ever goes anywhere but to AWS itself.

// AccessKeys is what signing in with keys takes.
type AccessKeys struct {
	Name            string `json:"name"`            // what to save them as; "" for the access key ID
	AccessKeyID     string `json:"accessKeyId"`     // AKIA…
	SecretAccessKey string `json:"secretAccessKey"` // never listed back out
	Region          string `json:"region"`          // e.g. "ap-south-1"
	Save            bool   `json:"save"`            // keep them on this computer
}

// SavedKeys is a saved pair as listed: the secret stays in the file.
type SavedKeys struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	AccessKeyID string    `json:"accessKeyId"` // shortened: AKIA…WXYZ
	Region      string    `json:"region"`
	LastUsed    time.Time `json:"lastUsed"`
}

// The file's entries, with the secret.
type savedKeys struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	AccessKeyID     string    `json:"accessKeyId"`
	SecretAccessKey string    `json:"secretAccessKey"`
	Region          string    `json:"region"`
	LastUsed        time.Time `json:"lastUsed"`
}

// How a keys connection is named as a "profile" (Connection.Profile,
// AWSIdentity.Profile): keys:<name>.
const keysPrefix = "keys:"

// KeySignIn checks an access key and secret key against AWS by listing the
// region's DynamoDB tables with them (what SpyDB needs them for), asks AWS
// who they belong to, keeps them for Connect, and saves them if asked.
func (s *Service) KeySignIn(ctx context.Context, k AccessKeys) (AWSIdentity, error) {
	k.AccessKeyID, k.SecretAccessKey, k.Name = strings.TrimSpace(k.AccessKeyID), strings.TrimSpace(k.SecretAccessKey), strings.TrimSpace(k.Name)
	if k.AccessKeyID == "" || k.SecretAccessKey == "" {
		return AWSIdentity{}, errors.New("Type both the access key ID and the secret access key.")
	}
	if k.Region == "" {
		k.Region = defaultRegion
	}
	if k.Name == "" {
		k.Name = k.AccessKeyID
	}
	ctx, done := s.cancellable(ctx)
	defer done()
	id, err := s.checkKeys(ctx, k)
	if err != nil {
		return AWSIdentity{}, cancelledOr(ctx, err)
	}
	if k.Save {
		if err := saveKeys(k); err != nil {
			return AWSIdentity{}, fmt.Errorf("Signed in, but couldn’t save the keys: %w", err)
		}
	}
	s.mu.Lock()
	s.keys = &k
	s.mu.Unlock()
	return id, nil
}

// SavedKeySignIn signs in with keys saved earlier (SavedKeys.ID).
func (s *Service) SavedKeySignIn(ctx context.Context, id string) (AWSIdentity, error) {
	all, err := readKeys()
	if err != nil {
		return AWSIdentity{}, err
	}
	i := slices.IndexFunc(all, func(k savedKeys) bool { return k.ID == id })
	if i < 0 {
		return AWSIdentity{}, errors.New("Those saved keys aren’t there any more.")
	}
	k := all[i]
	identity, err := s.KeySignIn(ctx, AccessKeys{Name: k.Name, AccessKeyID: k.AccessKeyID, SecretAccessKey: k.SecretAccessKey, Region: k.Region})
	if err != nil {
		return AWSIdentity{}, err
	}
	all[i].LastUsed = time.Now()
	_ = writeKeys(all) // only the time; a failure here isn't worth stopping for
	return identity, nil
}

// SavedAccessKeys lists the keys saved on this computer, most recently used first.
func (s *Service) SavedAccessKeys() ([]SavedKeys, error) {
	all, err := readKeys()
	if err != nil {
		return nil, err
	}
	out := make([]SavedKeys, 0, len(all))
	for _, k := range all {
		out = append(out, SavedKeys{ID: k.ID, Name: k.Name, AccessKeyID: shortKey(k.AccessKeyID), Region: k.Region, LastUsed: k.LastUsed})
	}
	slices.SortFunc(out, func(a, b SavedKeys) int { return b.LastUsed.Compare(a.LastUsed) })
	return out, nil
}

// DeleteSavedKeys forgets a saved pair.
func (s *Service) DeleteSavedKeys(id string) error {
	all, err := readKeys()
	if err != nil {
		return err
	}
	return writeKeys(slices.DeleteFunc(all, func(k savedKeys) bool { return k.ID == id }))
}

// checkKeys is the keys tried against AWS: DynamoDB in their region must
// answer (a table listing of one), and STS says who they are; if STS can't
// (a key allowed only DynamoDB, or a test endpoint), the identity is just
// the key.
func (s *Service) checkKeys(ctx context.Context, k AccessKeys) (AWSIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cfg, err := s.keysConfig(ctx, k)
	if err != nil {
		return AWSIdentity{}, err
	}
	ddb := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if s.endpoint != "" {
			o.BaseEndpoint = aws.String(s.endpoint)
		}
	})
	if _, err := ddb.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)}); err != nil {
		return AWSIdentity{}, fmt.Errorf("AWS didn’t accept these keys for DynamoDB in %s: %w", k.Region, awsError(err))
	}
	identity := AWSIdentity{Profile: keysPrefix + k.Name, Account: "", Arn: "access key " + shortKey(k.AccessKeyID)}
	if s.localDynamo() {
		return identity, nil
	}
	if out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err == nil {
		identity.Account, identity.Arn = aws.ToString(out.Account), aws.ToString(out.Arn)
	}
	return identity, nil
}

// keysConfig is the AWS library's settings for a pair of keys: those keys
// and the region win over anything in ~/.aws or the environment (which
// still supplies things like a local endpoint, AWS_ENDPOINT_URL_DYNAMODB).
func (s *Service) keysConfig(ctx context.Context, k AccessKeys) (aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(k.AccessKeyID, k.SecretAccessKey, "")),
		config.WithRegion(k.Region))
	if err != nil {
		return aws.Config{}, fmt.Errorf("Couldn't set up AWS's library: %w", err)
	}
	return cfg, nil
}

// localDynamo says whether DynamoDB is a local one (a test endpoint, or
// AWS_ENDPOINT_URL_DYNAMODB), where there's no AWS to ask who keys belong to.
func (s *Service) localDynamo() bool {
	return s.endpoint != "" || os.Getenv("AWS_ENDPOINT_URL_DYNAMODB") != ""
}

// shortKey is an access key ID for showing: its first 4 and last 4 characters.
func shortKey(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:4] + "…" + id[len(id)-4:]
}

func keysFile() (string, error) { return appdata.Path("aws-keys.json") }

func readKeys() ([]savedKeys, error) {
	path, err := keysFile()
	if err != nil {
		return nil, err
	}
	var all []savedKeys
	if err := appdata.ReadJSON(path, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func writeKeys(all []savedKeys) error {
	path, err := keysFile()
	if err != nil {
		return err
	}
	return appdata.WritePrivateJSON(path, all)
}

// saveKeys adds the pair, or replaces the saved pair of the same name.
func saveKeys(k AccessKeys) error {
	all, err := readKeys()
	if err != nil {
		return err
	}
	entry := savedKeys{ID: newKeysID(), Name: k.Name, AccessKeyID: k.AccessKeyID, SecretAccessKey: k.SecretAccessKey, Region: k.Region, LastUsed: time.Now()}
	if i := slices.IndexFunc(all, func(e savedKeys) bool { return e.Name == k.Name }); i >= 0 {
		entry.ID = all[i].ID
		all[i] = entry
	} else {
		all = append(all, entry)
	}
	return writeKeys(all)
}

func newKeysID() string {
	return fmt.Sprintf("k%d", time.Now().UnixNano())
}
