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
	_ "embed"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/cloudfoundry/cloud-service-broker/pkg/providers/tf"

	"github.com/cloudfoundry/cloud-service-broker/internal/brokerpak/manifest"
	"github.com/cloudfoundry/cloud-service-broker/internal/brokerpak/packer"
	"github.com/cloudfoundry/cloud-service-broker/internal/brokerpak/reader"
	"github.com/cloudfoundry/cloud-service-broker/pkg/broker"
	"github.com/cloudfoundry/cloud-service-broker/pkg/client"
	"github.com/cloudfoundry/cloud-service-broker/pkg/generator"
	"github.com/cloudfoundry/cloud-service-broker/pkg/server"
	"github.com/cloudfoundry/cloud-service-broker/utils/stream"
)

const manifestName = "manifest.yml"

//go:embed "examples/manifest.yml"
var exampleManifest []byte

// Init initializes a new brokerpak in the given directory with an example manifest and service definition.
func Init(directory string) error {
	if err := os.WriteFile(manifestName, exampleManifest, 0600); err != nil {
		return err
	}

	if err := stream.Copy(stream.FromYaml(tf.NewExampleTfServiceDefinition()), stream.ToFile(directory, "example-service-definition.yml")); err != nil {
		return err
	}

	return nil
}

// Pack creates a new brokerpak from the given directory which MUST contain a
// manifest.yml file. If the pack was successful, the returned string will be
// the path to the created brokerpak.
func Pack(directory string, cachePath string, includeSource bool) (string, error) {
	data, err := os.ReadFile(filepath.Join(directory, manifestName))
	if err != nil {
		return "", err
	}

	m, err := manifest.Parse(data)
	if err != nil {
		return "", err
	}

	version, ok := os.LookupEnv(m.Version)

	if !ok {
		version = m.Version
	}
	packname := fmt.Sprintf("%s-%s.brokerpak", m.Name, version)
	return packname, packer.Pack(m, directory, packname, cachePath, includeSource)
}

// Info writes out human-readable information about the brokerpak.
func Info(pack string) error {
	return finfo(pack, os.Stdout)
}

func finfo(pack string, out io.Writer) error {
	brokerPak, err := reader.OpenBrokerPak(pack)
	if err != nil {
		return err
	}

	mf, err := brokerPak.Manifest()
	if err != nil {
		return err
	}

	services, err := brokerPak.Services()
	if err != nil {
		return err
	}

	// Pack information
	fmt.Fprintln(out, "Information")
	{
		w := cmdTabWriter(out)
		fmt.Fprintf(w, "format\t%d\n", mf.PackVersion)
		fmt.Fprintf(w, "name\t%s\n", mf.Name)
		fmt.Fprintf(w, "version\t%s\n", mf.Version)
		fmt.Fprintln(w, "platforms")
		for _, arch := range mf.Platforms {
			fmt.Fprintf(w, "\t%s\n", arch.String())
		}
		fmt.Fprintln(w, "metadata")
		for k, v := range mf.Metadata {
			fmt.Fprintf(w, "\t%s\t%s\n", k, v)
		}

		w.Flush()
		fmt.Fprintln(out)
	}

	{
		fmt.Fprintln(out, "Parameters")
		w := cmdTabWriter(out)
		fmt.Fprintln(w, "NAME\tDESCRIPTION")
		for _, param := range mf.Parameters {
			fmt.Fprintf(w, "%s\t%s\n", param.Name, param.Description)
		}
		w.Flush()
		fmt.Fprintln(out)
	}
	{
		fmt.Fprintln(out, "Dependencies")
		w := cmdTabWriter(out)
		fmt.Fprintln(w, "NAME\tVERSION\tSOURCE")
		for _, resource := range mf.TerraformVersions {
			fmt.Fprintf(w, "%s\t%s\t%s\n", "terraform", resource.Version.String(), resource.Source)
		}
		for _, resource := range mf.TerraformProviders {
			fmt.Fprintf(w, "%s\t%s\t%s\n", resource.Name, resource.Version.String(), resource.Source)
		}
		for _, resource := range mf.Binaries {
			fmt.Fprintf(w, "%s\t%s\t%s\n", resource.Name, resource.Version, resource.Source)
		}
		w.Flush()
		fmt.Fprintln(out)
	}

	{
		fmt.Fprintln(out, "Services")
		w := cmdTabWriter(out)
		fmt.Fprintln(w, "ID\tNAME\tDESCRIPTION\tPLANS")
		for _, svc := range services {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", svc.ID, svc.Name, svc.Description, len(svc.Plans))
		}
		w.Flush()
		fmt.Println()
	}

	fmt.Fprintln(out, "Contents")
	sw := tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.StripEscape)
	fmt.Fprintln(sw, "MODE\tSIZE\tNAME")
	for _, fd := range brokerPak.Contents() {
		fmt.Fprintf(sw, "%s\t%d\t%s\n", fd.Mode().String(), fd.UncompressedSize64, fd.Name)
	}
	sw.Flush()
	fmt.Fprintln(out)

	return nil
}

func cmdTabWriter(out io.Writer) *tabwriter.Writer {
	// args: output, minwidth, tabwidth, padding, padchar, flags
	return tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.StripEscape)
}

// Validate checks the brokerpak for syntactic and limited semantic errors.
func Validate(pack string) error {
	brokerPak, err := reader.OpenBrokerPak(pack)
	if err != nil {
		return err
	}
	defer brokerPak.Close()

	return brokerPak.Validate()
}

// RegisterAll fetches all brokerpaks from the settings file and registers them
// with the given registry.
func RegisterAll(registry broker.BrokerRegistry) error {
	pakConfig, err := NewServerConfigFromEnv()
	if err != nil {
		return err
	}

	return NewRegistrar(pakConfig).Register(registry)
}

// RunExamples executes the examples from a brokerpak.
func RunExamples(pack string) {
	registry, err := registryFromLocalBrokerpak(pack)
	if err != nil {
		log.Fatalf("Error executing examples (registry): %v", err)
	}

	apiClient, err := client.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Error executing examples (client): %v", err)
	}

	allExamples, err := server.GetAllCompleteServiceExamples(registry)
	if err != nil {
		log.Fatalf("Error executing examples (getting): %v", err)
	}

	client.RunExamplesForService(allExamples, apiClient, "", "", 1)
}

// Docs generates the markdown usage docs for the given pack and writes them to stdout.
func Docs(pack string) error {
	registry, err := registryFromLocalBrokerpak(pack)
	if err != nil {
		fmt.Println(err)
		return err
	}

	fmt.Println(generator.CatalogDocumentation(registry))
	return nil
}

func registryFromLocalBrokerpak(packPath string) (broker.BrokerRegistry, error) {
	config := newLocalFileServerConfig(packPath)

	registry := broker.BrokerRegistry{}
	if err := NewRegistrar(config).Register(registry); err != nil {
		return nil, err
	}

	return registry, nil
}
