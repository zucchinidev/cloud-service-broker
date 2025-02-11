// Copyright 2018 the Service Broker Project Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"code.cloudfoundry.org/lager"
	"github.com/spf13/viper"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
)

const (
	EnvironmentVarPrefix = "gsb"
	rootSaEnvVar         = "ROOT_SERVICE_ACCOUNT_JSON"
	cloudPlatformScope   = "https://www.googleapis.com/auth/cloud-platform"
)

var (
	PropertyToEnvReplacer = strings.NewReplacer(".", "_", "-", "_")

	// InvalidLabelChars encodes that GCP labels only support alphanumeric,
	// dash and underscore characters in keys and values.
	InvalidLabelChars = regexp.MustCompile("[^a-zA-Z0-9_-]+")
)

func init() {
	viper.BindEnv("google.account", rootSaEnvVar)
}

func GetAuthedConfig() (*jwt.Config, error) {
	rootCreds := GetServiceAccountJSON()
	conf, err := google.JWTConfigFromJSON([]byte(rootCreds), cloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("error initializing config from credentials: %s", err)
	}
	return conf, nil
}

// PrettyPrintOrExit writes a JSON serialized version of the content to stdout.
// If a failure occurs during marshaling, the error is logged along with a
// formatted version of the object and the program exits with a failure status.
func PrettyPrintOrExit(content interface{}) {
	err := prettyPrint(content)

	if err != nil {
		log.Fatalf("Could not format results: %s, results were: %+v", err, content)
	}
}

// PrettyPrintOrErr writes a JSON serialized version of the content to stdout.
// If a failure occurs during marshaling, the error is logged along with a
// formatted version of the object and the function will return the error.
func PrettyPrintOrErr(content interface{}) error {
	err := prettyPrint(content)

	if err != nil {
		log.Printf("Could not format results: %s, results were: %+v", err, content)
	}

	return err
}

func prettyPrint(content interface{}) error {
	prettyResults, err := json.MarshalIndent(content, "", "    ")
	if err == nil {
		fmt.Println(string(prettyResults))
	}

	return err
}

// PropertyToEnv converts a Viper configuration property name into an
// environment variable prefixed with EnvironmentVarPrefix
func PropertyToEnv(propertyName string) string {
	return PropertyToEnvUnprefixed(EnvironmentVarPrefix + "." + propertyName)
}

// PropertyToEnvUnprefixed converts a Viper configuration property name into an
// environment variable using PropertyToEnvReplacer
func PropertyToEnvUnprefixed(propertyName string) string {
	return PropertyToEnvReplacer.Replace(strings.ToUpper(propertyName))
}

// SetParameter sets a value on a JSON raw message and returns a modified
// version with the value set
func SetParameter(input json.RawMessage, key string, value interface{}) (json.RawMessage, error) {
	params := make(map[string]interface{})

	if len(input) != 0 {
		err := json.Unmarshal(input, &params)
		if err != nil {
			return nil, err
		}
	}

	params[key] = value

	return json.Marshal(params)
}

// UnmarshalObjectRemainder unmarshals an object into v and returns the
// remaining key/value pairs as a JSON string by doing a set difference.
func UnmarshalObjectRemainder(data []byte, v interface{}) ([]byte, error) {
	if err := json.Unmarshal(data, v); err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return jsonDiff(data, encoded)
}

func jsonDiff(superset, subset json.RawMessage) ([]byte, error) {
	usedKeys := make(map[string]json.RawMessage)
	if err := json.Unmarshal(subset, &usedKeys); err != nil {
		return nil, err
	}

	allKeys := make(map[string]json.RawMessage)
	if err := json.Unmarshal(superset, &allKeys); err != nil {
		return nil, err
	}

	remainder := make(map[string]json.RawMessage)
	for key, value := range allKeys {
		if _, ok := usedKeys[key]; !ok {
			remainder[key] = value
		}
	}

	return json.Marshal(remainder)
}

// GetDefaultProjectID gets the default project id for the service broker based
// on the JSON Service Account key.
func GetDefaultProjectID() (string, error) {
	serviceAccount := make(map[string]string)
	if err := json.Unmarshal([]byte(GetServiceAccountJSON()), &serviceAccount); err != nil {
		return "", fmt.Errorf("could not unmarshal service account details. %v", err)
	}

	return serviceAccount["project_id"], nil
}

// GetServiceAccountJSON gets the raw JSON credentials of the Service Account
// the service broker acts as.
func GetServiceAccountJSON() string {
	return viper.GetString("google.account")
}

// SingleLineErrorFormatter creates a single line error string from an array of errors.
func SingleLineErrorFormatter(es []error) string {
	points := make([]string, len(es))
	for i, err := range es {
		points[i] = err.Error()
	}

	return fmt.Sprintf("%d error(s) occurred: %s", len(es), strings.Join(points, "; "))
}

// NewLogger creates a new lager.Logger with the given name that has correct
// writing settings.
func NewLogger(name string) lager.Logger {
	logger := lager.NewLogger(name)

	logLevel := lager.INFO
	if _, debug := os.LookupEnv("GSB_DEBUG"); debug {
		logLevel = lager.DEBUG
	}

	logger.RegisterSink(lager.NewWriterSink(os.Stderr, lager.ERROR))
	logger.RegisterSink(lager.NewWriterSink(os.Stdout, logLevel))

	return logger
}

// SplitNewlineDelimitedList splits a list of newline delimited items and trims
// any leading or trailing whitespace from them.
func SplitNewlineDelimitedList(paksText string) []string {
	var out []string
	for _, pak := range strings.Split(paksText, "\n") {
		pakURL := strings.TrimSpace(pak)
		if pakURL != "" {
			out = append(out, pakURL)
		}
	}

	return out
}

// Indent indents every line of the given text with the given string.
func Indent(text, by string) string {
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		lines[i] = by + line
	}

	return strings.Join(lines, "\n")
}

// CopyStringMap makes a copy of the given map.
func CopyStringMap(m map[string]string) map[string]string {
	out := make(map[string]string)

	for k, v := range m {
		out[k] = v
	}

	return out
}
