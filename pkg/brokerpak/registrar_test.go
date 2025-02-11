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

package brokerpak

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hashicorp/go-version"

	"github.com/cloudfoundry/cloud-service-broker/pkg/providers/tf"
	"github.com/cloudfoundry/cloud-service-broker/pkg/providers/tf/executor"

	"github.com/cloudfoundry/cloud-service-broker/internal/brokerpak/manifest"
	"github.com/cloudfoundry/cloud-service-broker/pkg/broker"
	"github.com/cloudfoundry/cloud-service-broker/pkg/varcontext"
	"github.com/spf13/viper"
)

func TestNewRegistrar(t *testing.T) {
	// Create a dummy brokerpak
	pk, err := fakeBrokerpak()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pk)

	abs, err := filepath.Abs(pk)
	if err != nil {
		t.Fatal(err)
	}

	config := newLocalFileServerConfig(abs)
	registry := broker.BrokerRegistry{}
	err = NewRegistrar(config).Register(registry)
	if err != nil {
		t.Fatal(err)
	}

	if len(registry) != 1 {
		t.Fatal("Expected length to be 1 got", len(registry))
	}

	os.Setenv("ENV_VAR", "something")
	defer os.Unsetenv("ENV_VAR")
	if viper.GetString("env.var") != "something" {
		t.Fatal("env var mapping did not happen")
	}
}

func TestRegistrar_toDefinitions(t *testing.T) {
	fakeDefn := func(name, id string) tf.TfServiceDefinitionV1 {
		ex := tf.NewExampleTfServiceDefinition()
		ex.ID = id
		ex.Name = "service-" + name

		return ex
	}

	goodCases := map[string]struct {
		Services      []tf.TfServiceDefinitionV1
		Config        BrokerpakSourceConfig
		ExpectedNames []string
	}{
		"straight though": {
			Services: []tf.TfServiceDefinitionV1{
				fakeDefn("foo", "b69a96ad-0c38-4e84-84a3-be9513e3c645"),
				fakeDefn("bar", "f71f1327-2bce-41b4-a833-0ec6430dd7ca"),
			},
			Config: BrokerpakSourceConfig{
				ExcludedServices: "",
				ServicePrefix:    "",
			},
			ExpectedNames: []string{"service-foo", "service-bar"},
		},
		"prefix": {
			Services: []tf.TfServiceDefinitionV1{
				fakeDefn("foo", "b69a96ad-0c38-4e84-84a3-be9513e3c645"),
				fakeDefn("bar", "f71f1327-2bce-41b4-a833-0ec6430dd7ca"),
			},
			Config: BrokerpakSourceConfig{
				ExcludedServices: "",
				ServicePrefix:    "pre-",
			},
			ExpectedNames: []string{"pre-service-foo", "pre-service-bar"},
		},
		"exclude-foo": {
			Services: []tf.TfServiceDefinitionV1{
				fakeDefn("foo", "b69a96ad-0c38-4e84-84a3-be9513e3c645"),
				fakeDefn("bar", "f71f1327-2bce-41b4-a833-0ec6430dd7ca"),
			},
			Config: BrokerpakSourceConfig{
				ExcludedServices: "b69a96ad-0c38-4e84-84a3-be9513e3c645",
				ServicePrefix:    "",
			},
			ExpectedNames: []string{"service-bar"},
		},
	}

	for tn, tc := range goodCases {
		t.Run(tn, func(t *testing.T) {
			r := NewRegistrar(nil)
			defns, err := r.toDefinitions(tc.Services, tc.Config, executor.TFBinariesContext{DefaultTfVersion: version.Must(version.NewVersion("0.0.0"))}, nil)
			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}

			var actualNames []string
			for _, defn := range defns {
				actualNames = append(actualNames, defn.Name)
			}

			if !reflect.DeepEqual(actualNames, tc.ExpectedNames) {
				t.Errorf("Expected names to be %v, got %v", tc.ExpectedNames, actualNames)
			}
		})
	}

	badCases := map[string]struct {
		Services      []tf.TfServiceDefinitionV1
		Config        BrokerpakSourceConfig
		ExpectedError string
	}{
		"bad service": {
			Services: []tf.TfServiceDefinitionV1{
				fakeDefn("foo", "bad uuid"),
			},
			Config:        BrokerpakSourceConfig{},
			ExpectedError: "field must be a UUID: id",
		},
	}

	for tn, tc := range badCases {
		t.Run(tn, func(t *testing.T) {
			r := NewRegistrar(nil)
			defns, err := r.toDefinitions(tc.Services, tc.Config, executor.TFBinariesContext{}, nil)
			if err == nil {
				t.Fatal("Expected error, got: <nil>")
			}

			if defns != nil {
				t.Errorf("Expected defns to be nil got %v", defns)
			}

			if err.Error() != tc.ExpectedError {
				t.Errorf("Expected error to be %q got %v", tc.ExpectedError, err)
			}
		})
	}
}

func TestResolveParameters(t *testing.T) {
	cases := map[string]struct {
		Context  map[string]interface{}
		Params   []manifest.Parameter
		Expected map[string]string
	}{
		"no-params": {
			Context:  map[string]interface{}{"n": 1, "s": "two", "b": true},
			Params:   []manifest.Parameter{},
			Expected: map[string]string{},
		},
		"missing-in-context": {
			Context: map[string]interface{}{"n": 1, "s": "two", "b": true},
			Params: []manifest.Parameter{
				{Name: "foo", Description: "some missing param"},
			},
			Expected: map[string]string{},
		},
		"contained-in-context": {
			Context: map[string]interface{}{"n": 1, "s": "two", "b": true},
			Params: []manifest.Parameter{
				{Name: "s", Description: "a string param"},
				{Name: "b", Description: "a bool param"},
				{Name: "n", Description: "a numeric param"},
			},
			Expected: map[string]string{"s": "two", "b": "true", "n": "1"},
		},
	}

	for tn, tc := range cases {
		t.Run(tn, func(t *testing.T) {
			vc, err := varcontext.Builder().MergeMap(tc.Context).Build()
			if err != nil {
				t.Fatal(err)
			}

			actual := resolveParameters(tc.Params, vc)
			if !reflect.DeepEqual(actual, tc.Expected) {
				t.Errorf("Expected params to be: %v got %v", tc.Expected, actual)
			}
		})
	}
}

func TestRegistrar_walk(t *testing.T) {
	goodCases := map[string]struct {
		Config   *ServerConfig
		Expected map[string]map[string]interface{}
	}{
		"basic": {
			Config: &ServerConfig{
				Config: `{}`,
				Brokerpaks: map[string]BrokerpakSourceConfig{
					"example": {Config: `{}`},
				},
			},
			Expected: map[string]map[string]interface{}{
				"example": {},
			},
		},
		"server-config": {
			Config: &ServerConfig{
				Config: `{"foo":"bar"}`,
				Brokerpaks: map[string]BrokerpakSourceConfig{
					"example": {Config: `{}`},
				},
			},
			Expected: map[string]map[string]interface{}{
				"example": {"foo": "bar"},
			},
		},
		"override": {
			Config: &ServerConfig{
				Config: `{"foo":"bar"}`,
				Brokerpaks: map[string]BrokerpakSourceConfig{
					"example": {Config: `{"foo":"bazz"}`},
				},
			},
			Expected: map[string]map[string]interface{}{
				"example": {"foo": "bazz"},
			},
		},
		"additive configs": {
			Config: &ServerConfig{
				Config: `{"foo":"bar"}`,
				Brokerpaks: map[string]BrokerpakSourceConfig{
					"example": {Config: `{"bar":"bazz"}`},
				},
			},
			Expected: map[string]map[string]interface{}{
				"example": {"foo": "bar", "bar": "bazz"},
			},
		},
	}

	for tn, tc := range goodCases {
		t.Run(tn, func(t *testing.T) {
			actual := make(map[string]map[string]interface{})
			err := NewRegistrar(tc.Config).walk(func(name string, pak BrokerpakSourceConfig, vc *varcontext.VarContext) error {
				actual[name] = vc.ToMap()
				return nil
			})

			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}

			if !reflect.DeepEqual(tc.Expected, actual) {
				t.Errorf("Expected %v got %v", tc.Expected, actual)
			}
		})
	}

	badCases := map[string]struct {
		Config   *ServerConfig
		Expected string
	}{
		"bad global config": {
			Config: &ServerConfig{
				Config: `a`,
				Brokerpaks: map[string]BrokerpakSourceConfig{
					"example": {Config: `{}`},
				},
			},
			Expected: `couldn't merge config for brokerpak "example": 1 error(s) occurred: invalid character 'a' looking for beginning of value`,
		},
		"bad local config": {
			Config: &ServerConfig{
				Config: `{}`,
				Brokerpaks: map[string]BrokerpakSourceConfig{
					"example": {Config: `b`},
				},
			},
			Expected: `couldn't merge config for brokerpak "example": 1 error(s) occurred: invalid character 'b' looking for beginning of value`,
		},
		"walk error": {
			Config: &ServerConfig{
				Config: `{}`,
				Brokerpaks: map[string]BrokerpakSourceConfig{
					"example": {Config: `{}`},
				},
			},
			Expected: "walk raised error",
		},
	}

	for tn, tc := range badCases {
		t.Run(tn, func(t *testing.T) {
			err := NewRegistrar(tc.Config).walk(func(name string, pak BrokerpakSourceConfig, vc *varcontext.VarContext) error {
				return errors.New("walk raised error")
			})

			if err == nil {
				t.Fatalf("Expected error %q, got: nil", tc.Expected)
			}

			if tc.Expected != err.Error() {
				t.Errorf("Expected: %q got: %q", tc.Expected, err.Error())
			}
		})
	}
}
