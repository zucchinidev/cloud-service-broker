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

package reader

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cloudfoundry/cloud-service-broker/pkg/providers/tf"

	"github.com/cloudfoundry/cloud-service-broker/internal/brokerpak/fetcher"
	"github.com/cloudfoundry/cloud-service-broker/internal/brokerpak/manifest"
	"github.com/cloudfoundry/cloud-service-broker/internal/brokerpak/platform"
	"github.com/cloudfoundry/cloud-service-broker/internal/zippy"
	"github.com/cloudfoundry/cloud-service-broker/utils/stream"
	"github.com/hashicorp/go-version"
)

const manifestName = "manifest.yml"

// OpenBrokerPak opens the file at the given path as a BrokerPakReader.
func OpenBrokerPak(pakPath string) (*BrokerPakReader, error) {
	rc, err := zippy.Open(pakPath)
	if err != nil {
		return nil, err
	}
	return &BrokerPakReader{contents: rc}, nil
}

// DownloadAndOpenBrokerpak downloads a (potentially remote) brokerpak to
// the local filesystem and opens it.
func DownloadAndOpenBrokerpak(pakURI string) (*BrokerPakReader, error) {
	// create a temp directory to hold the pak
	pakDir, err := os.MkdirTemp("", "brokerpak-staging")
	if err != nil {
		return nil, fmt.Errorf("couldn't create brokerpak staging area for %q: %v", pakURI, err)
	}

	// Download the brokerpak
	localLocation := filepath.Join(pakDir, "pack.brokerpak")
	if err := fetcher.FetchBrokerpak(pakURI, localLocation); err != nil {
		return nil, fmt.Errorf("couldn't download brokerpak %q: %v", pakURI, err)
	}

	return OpenBrokerPak(localLocation)
}

// BrokerPakReader reads bundled together Terraform and service definitions.
type BrokerPakReader struct {
	contents zippy.ZipReader
}

// Manifest fetches the manifest out of the package.
func (pak *BrokerPakReader) Manifest() (*manifest.Manifest, error) {
	data, err := pak.readBytes(manifestName)
	if err != nil {
		return nil, err
	}
	return manifest.Parse(data)
}

// Services gets the list of services included in the pack.
func (pak *BrokerPakReader) Services() ([]tf.TfServiceDefinitionV1, error) {
	manifest, err := pak.Manifest()
	if err != nil {
		return nil, err
	}

	var services []tf.TfServiceDefinitionV1
	for _, serviceDefinition := range manifest.ServiceDefinitions {
		var receiver tf.TfServiceDefinitionV1
		if err := pak.readYaml(serviceDefinition, &receiver); err != nil {
			return nil, err
		}

		receiver.RequiredEnvVars = manifest.RequiredEnvVars
		services = append(services, receiver)
	}

	return services, nil
}

// Validate checks the manifest service definitions for syntactic and
// limited semantic errors. The manifest has previously been validated during parsing.
func (pak *BrokerPakReader) Validate() error {
	services, err := pak.Services()
	if err != nil {
		return fmt.Errorf("couldn't list services: %v", err)
	}

	for _, svc := range services {
		if err := svc.Validate(); err != nil {
			return fmt.Errorf("service %q failed validation: %v", svc.Name, err)
		}
	}

	return nil
}

// Close closes the underlying reader for the BrokerPakReader.
func (pak *BrokerPakReader) Close() error {
	pak.contents.Close()
	return nil
}

func (pak *BrokerPakReader) Contents() []*zip.File {
	return pak.contents.List()
}

// ExtractPlatformBins extracts the binaries for the current platform to the
// given destination.
func (pak *BrokerPakReader) ExtractPlatformBins(destination string) error {
	mf, err := pak.Manifest()
	if err != nil {
		return err
	}

	if !mf.AppliesToCurrentPlatform() {
		return fmt.Errorf("the package %q doesn't contain binaries compatible with the current platform %q", mf.Name, platform.CurrentPlatform().String())
	}

	terraformVersion, err := mf.DefaultTerraformVersion()
	if err != nil {
		return err
	}

	for _, r := range mf.TerraformProviders {
		if err := pak.extractProvider(r, destination, terraformVersion); err != nil {
			return err
		}
	}
	for _, r := range mf.TerraformVersions {
		if err := pak.extractTerraform(r, destination); err != nil {
			return err
		}
	}
	for _, r := range mf.Binaries {
		if err := pak.extractBinary(r, destination); err != nil {
			return err
		}
	}

	return nil
}

func (pak *BrokerPakReader) extractProvider(r manifest.TerraformProvider, destination string, terraformVersion *version.Version) error {
	filePath, err := pak.findFileInZip(fmt.Sprintf("%s_v%s", r.Name, r.Version))
	if err != nil {
		return err
	}

	if err := pak.contents.ExtractFile(filePath, providerInstallPath(terraformVersion, destination, r)); err != nil {
		return fmt.Errorf("error extracting terraform-provider file: %w", err)
	}

	return nil
}

func (pak *BrokerPakReader) extractBinary(r manifest.Binary, destination string) error {
	filePath, err := pak.findFileInZip(r.Name)
	if err != nil {
		return err
	}

	if err := pak.contents.ExtractFile(filePath, destination); err != nil {
		return fmt.Errorf("error extracting binary file: %w", err)
	}

	return nil
}

func (pak *BrokerPakReader) extractTerraform(r manifest.TerraformVersion, destination string) error {
	plat := platform.CurrentPlatform()
	versionedPath := path.Join("bin", plat.Os, plat.Arch, r.Version.String(), "terraform")
	if pak.fileExistsInZip(versionedPath) {
		if err := pak.contents.ExtractFile(versionedPath, filepath.Join(destination, "versions", r.Version.String())); err != nil {
			return fmt.Errorf("error extracting versioned terraform binary: %w", err)
		}

		return nil
	}

	// For compatibility with brokerpaks built with older versions
	unversionedPath := path.Join("bin", plat.Os, plat.Arch, "terraform")
	if pak.fileExistsInZip(unversionedPath) {
		if err := pak.contents.ExtractFile(unversionedPath, filepath.Join(destination, "versions", r.Version.String())); err != nil {
			return fmt.Errorf("error extracting terraform binary: %w", err)
		}

		return nil
	}

	return fmt.Errorf("could not find Terraform version %s in brokerpak", r.Version)
}

func (pak *BrokerPakReader) findFileInZip(name string) (string, error) {
	plat := platform.CurrentPlatform()
	prefix := path.Join("bin", plat.Os, plat.Arch, name)
	var found []string

	for _, f := range pak.contents.List() {
		if strings.HasPrefix(f.Name, prefix) {
			found = append(found, f.Name)
		}
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("file with prefix %q not found in zip", prefix)
	default:
		return "", fmt.Errorf("multiple files found with prefix %q: %s", prefix, strings.Join(found, ", "))
	}
}

func (pak *BrokerPakReader) fileExistsInZip(path string) bool {
	for _, f := range pak.contents.List() {
		if f.Name == path {
			return true
		}
	}

	return false
}

func (pak *BrokerPakReader) readYaml(name string, v interface{}) error {
	fd := pak.contents.Find(name)
	if fd == nil {
		return fmt.Errorf("couldn't find the file with the name %q", name)
	}

	return stream.Copy(stream.FromReadCloserError(fd.Open()), stream.ToYaml(v))
}

func (pak *BrokerPakReader) readBytes(name string) ([]byte, error) {
	fd := pak.contents.Find(name)
	if fd == nil {
		return nil, fmt.Errorf("couldn't find the file with the name %q", name)
	}

	r, err := fd.Open()
	if err != nil {
		return nil, fmt.Errorf("could not open file: %w", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}
	return data, nil
}

func providerInstallPath(terraformVersion *version.Version, destination string, tfProvider manifest.TerraformProvider) string {
	if terraformVersion.LessThan(version.Must(version.NewVersion("0.13.0"))) {
		return destination
	}

	plat := platform.CurrentPlatform()
	return filepath.Join(
		destination,
		tfProvider.Provider.String(),
		tfProvider.Version.String(),
		fmt.Sprintf("%s_%s", plat.Os, plat.Arch),
	)
}
