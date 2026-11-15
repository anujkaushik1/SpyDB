// Command fakeaws stands in for the AWS CLI in the end-to-end tests
// (e2e/run.mjs), on every system: SpyDB signs in to DynamoDB through the AWS
// CLI, and this answers the two questions it asks with a made-up account,
// so no test ever reaches AWS. The data itself comes from DynamoDB Local.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	switch {
	case len(args) == 1 && args[0] == "--version":
		fmt.Println("aws-cli/2.40.0 Python/3.14.8 fake/0 exe/fake")
	case len(args) >= 2 && args[0] == "sts" && args[1] == "get-caller-identity": // with --profile, --output json
		fmt.Println(`{"Account": "111122223333", "Arn": "arn:aws:iam::111122223333:user/demo-user"}`)
	default:
		fmt.Fprintln(os.Stderr, "fakeaws: not faked:", strings.Join(args, " "))
		os.Exit(2)
	}
}
