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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"code.cloudfoundry.org/lager"
	"github.com/cloudfoundry-incubator/cloud-service-broker/internal/brokerpak/manifest"
	"github.com/cloudfoundry-incubator/cloud-service-broker/internal/brokerpak/reader"
	"github.com/cloudfoundry-incubator/cloud-service-broker/pkg/broker"
	"github.com/cloudfoundry-incubator/cloud-service-broker/pkg/providers/tf"
	"github.com/cloudfoundry-incubator/cloud-service-broker/pkg/providers/tf/wrapper"
	"github.com/cloudfoundry-incubator/cloud-service-broker/pkg/varcontext"
	"github.com/cloudfoundry-incubator/cloud-service-broker/utils"
	"github.com/spf13/cast"
	"github.com/spf13/viper"
)

type registrarWalkFunc func(name string, pak BrokerpakSourceConfig, vc *varcontext.VarContext) error

// Registrar is responsible for registering brokerpaks with BrokerRegistries
// subject to the settings provided by a ServerConfig like injecting
// environment variables and skipping certain services.
type Registrar struct {
	config *ServerConfig
}

// Register fetches the brokerpaks and registers them with the given registry.
func (r *Registrar) Register(registry broker.BrokerRegistry) error {
	registerLogger := utils.NewLogger("brokerpak-registration")

	return r.walk(func(name string, pak BrokerpakSourceConfig, vc *varcontext.VarContext) error {
		registerLogger.Info("registering", lager.Data{
			"name":              name,
			"location":          pak.BrokerpakUri,
			"notes":             pak.Notes,
			"excluded-services": pak.ExcludedServicesSlice(),
			"prefix":            pak.ServicePrefix,
		})

		brokerPak, err := reader.DownloadAndOpenBrokerpak(pak.BrokerpakUri)
		if err != nil {
			return fmt.Errorf("couldn't open brokerpak: %q: %v", pak.BrokerpakUri, err)
		}
		defer brokerPak.Close()

		executor, err := r.createExecutor(brokerPak, vc)
		if err != nil {
			return err
		}

		// register the services
		services, err := brokerPak.Services()
		if err != nil {
			return err
		}

		defns, err := r.toDefinitions(services, pak, executor)
		if err != nil {
			return err
		}

		for _, defn := range defns {
			err := registry.Register(defn)
			if err != nil {
				return err
			}
		}

		errs := registry.Validate()
		if errs != nil {
			return errs
		}

		if manifest, err := brokerPak.Manifest(); err == nil {
			for env, config := range manifest.EnvConfigMapping {
				viper.BindEnv(config, env)
			}
		}

		return nil
	})
}

func (Registrar) toDefinitions(services []tf.TfServiceDefinitionV1, config BrokerpakSourceConfig, executor wrapper.TerraformExecutor) ([]*broker.ServiceDefinition, error) {
	var out []*broker.ServiceDefinition

	toIgnore := utils.NewStringSet(config.ExcludedServicesSlice()...)
	for _, svc := range services {
		if toIgnore.Contains(svc.Id) {
			continue
		}

		svc.Name = config.ServicePrefix + svc.Name

		bs, err := svc.ToService(executor)
		if err != nil {
			return nil, err
		}

		out = append(out, bs)
	}

	return out, nil
}

func (r *Registrar) createExecutor(brokerPak *reader.BrokerPakReader, vc *varcontext.VarContext) (wrapper.TerraformExecutor, error) {
	dir, err := os.MkdirTemp("", "brokerpak")
	if err != nil {
		return nil, err
	}

	// extract the Terraform directory
	if err := brokerPak.ExtractPlatformBins(dir); err != nil {
		return nil, err
	}

	manifest, err := brokerPak.Manifest()
	if err != nil {
		return nil, err
	}
	tfVersion, err := manifest.DefaultTerraformVersion()
	if err != nil {
		return nil, err
	}

	binPath := filepath.Join(dir, "terraform")
	executor := wrapper.CustomTerraformExecutor(binPath, dir, tfVersion, wrapper.DefaultExecutor)

	params := r.resolveParameters(manifest.Parameters, vc)
	executor = wrapper.CustomEnvironmentExecutor(params, executor)

	return executor, nil
}

// resolveParameters resolves environment variables from the given global and
// brokerpak specific.
func (Registrar) resolveParameters(params []manifest.Parameter, vc *varcontext.VarContext) map[string]string {
	out := make(map[string]string)

	context := vc.ToMap()
	for _, p := range params {
		val, ok := context[p.Name]
		if ok {
			out[p.Name] = cast.ToString(val)
		}
	}

	return out
}

func (r *Registrar) walk(callback registrarWalkFunc) error {
	for name, pak := range r.config.Brokerpaks {
		vc, err := varcontext.Builder().
			MergeJsonObject(json.RawMessage(r.config.Config)).
			MergeJsonObject(json.RawMessage(pak.Config)).
			Build()

		if err != nil {
			return fmt.Errorf("couldn't merge config for brokerpak %q: %v", name, err)
		}

		if err := callback(name, pak, vc); err != nil {
			return err
		}
	}

	return nil
}

// NewRegistrar constructs a new registrar with the given configuration.
// Registrar expects to become the owner of the configuration afterwards.
func NewRegistrar(sc *ServerConfig) *Registrar {
	return &Registrar{config: sc}
}
