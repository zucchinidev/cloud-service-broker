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

package workspace

import "fmt"

func ExampleModuleInstance_MarshalDefinition() {
	instance := ModuleInstance{
		ModuleName:    "foo-module",
		InstanceName:  "instance",
		Configuration: map[string]interface{}{"foo": "bar"},
	}

	outputs := []string{"output1", "output2"}

	defnJSON, err := instance.MarshalDefinition(outputs)
	fmt.Println(err)
	fmt.Printf("%s\n", string(defnJSON))

	// Output: <nil>
	// {"module":{"instance":{"foo":"bar","source":"./foo-module"}},"output":{"output1":{"value":"${module.instance.output1}"},"output2":{"value":"${module.instance.output2}"}}}
}

func ExampleModuleInstance_MarshalDefinition_emptyOutputs() {
	instance := ModuleInstance{
		ModuleName:    "foo-module",
		InstanceName:  "instance",
		Configuration: map[string]interface{}{"foo": "bar"},
	}

	defnJSON, err := instance.MarshalDefinition([]string{})
	fmt.Println(err)
	fmt.Printf("%s\n", string(defnJSON))

	// Output: <nil>
	// {"module":{"instance":{"foo":"bar","source":"./foo-module"}}}
}
