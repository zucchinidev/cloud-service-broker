// Copyright 2019 the Service Broker Project Authors.
// Copyright 2017 The Knative Authors
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

// DO NOT EDIT: This file was adapted from github.com/knative/pkg

package validation

import (
	"fmt"
	"sort"
	"strings"
)

// CurrentField is a constant to supply as a fieldPath for when there is
// a problem with the current field itself.
const CurrentField = ""

// FieldError is used to propagate the context of errors pertaining to
// specific fields in a manner suitable for use in a recursive walk, so
// that errors contain the appropriate field context.
// FieldError methods are non-mutating.
// +k8s:deepcopy-gen=true
type FieldError struct {
	Message string
	Paths   []string
	// Details contains an optional longer payload.
	// +optional
	Details string
	errors  []FieldError
}

// FieldError implements error
var _ error = (*FieldError)(nil)

// ViaField is used to propagate a validation error along a field access.
// For example, if a type recursively validates its "spec" via:
//   if err := foo.Spec.Validate(); err != nil {
//     // Augment any field paths with the context that they were accessed
//     // via "spec".
//     return err.ViaField("spec")
//   }
func (e *FieldError) ViaField(prefix ...string) *FieldError {
	if e == nil {
		return nil
	}
	// Copy over message and details, paths will be updated and errors come
	// along using .Also().
	newErr := &FieldError{
		Message: e.Message,
		Details: e.Details,
	}

	// Prepend the Prefix to existing errors.
	newPaths := make([]string, 0, len(e.Paths))
	for _, oldPath := range e.Paths {
		newPaths = append(newPaths, flatten(append(prefix, oldPath)))
	}
	newErr.Paths = newPaths
	for _, e := range e.errors {
		newErr = newErr.Also(e.ViaField(prefix...))
	}
	return newErr
}

// ViaIndex is used to attach an index to the next ViaField provided.
// For example, if a type recursively validates a parameter that has a collection:
//  for i, c := range spec.Collection {
//    if err := doValidation(c); err != nil {
//      return err.ViaIndex(i).ViaField("collection")
//    }
//  }
func (e *FieldError) ViaIndex(index int) *FieldError {
	return e.ViaField(asIndex(index))
}

// ViaFieldIndex is the short way to chain: err.ViaIndex(bar).ViaField(foo)
func (e *FieldError) ViaFieldIndex(field string, index int) *FieldError {
	return e.ViaIndex(index).ViaField(field)
}

// ViaKey is used to attach a key to the next ViaField provided.
// For example, if a type recursively validates a parameter that has a collection:
//  for k, v := range spec.Bag {
//    if err := doValidation(v); err != nil {
//      return err.ViaKey(k).ViaField("bag")
//    }
//  }
func (e *FieldError) ViaKey(key string) *FieldError {
	return e.ViaField(asKey(key))
}

// ViaFieldKey is the short way to chain: err.ViaKey(bar).ViaField(foo)
func (e *FieldError) ViaFieldKey(field string, key string) *FieldError {
	return e.ViaKey(key).ViaField(field)
}

// Also collects errors, returns a new collection of existing errors and new errors.
func (e *FieldError) Also(errs ...*FieldError) *FieldError {
	var newErr *FieldError
	// collect the current objects errors, if it has any
	if !e.isEmpty() {
		newErr = e.DeepCopy()
	} else {
		newErr = &FieldError{}
	}
	// and then collect the passed in errors
	for _, e := range errs {
		if !e.isEmpty() {
			newErr.errors = append(newErr.errors, *e)
		}
	}
	if newErr.isEmpty() {
		return nil
	}
	return newErr
}

func (e *FieldError) isEmpty() bool {
	if e == nil {
		return true
	}
	return e.Message == "" && e.Details == "" && len(e.errors) == 0 && len(e.Paths) == 0
}

// normalized returns a flattened copy of all the errors.
func (e *FieldError) normalized() []*FieldError {
	// In case we call normalized on a nil object, return just an empty
	// list. This can happen when .Error() is called on a nil object.
	if e == nil {
		return []*FieldError(nil)
	}

	// Allocate errors with at least as many objects as we'll get on the first pass.
	errors := make([]*FieldError, 0, len(e.errors)+1)
	// If this FieldError is a leaf, add it.
	if e.Message != "" {
		errors = append(errors, &FieldError{
			Message: e.Message,
			Paths:   e.Paths,
			Details: e.Details,
		})
	}
	// And then collect all other errors recursively.
	for _, e := range e.errors {
		errors = append(errors, e.normalized()...)
	}
	return errors
}

// Error implements error
func (e *FieldError) Error() string {
	// Get the list of errors as a flat merged list.
	normedErrors := merge(e.normalized())
	errs := make([]string, 0, len(normedErrors))
	for _, e := range normedErrors {
		if e.Details == "" {
			errs = append(errs, fmt.Sprintf("%v: %v", e.Message, strings.Join(e.Paths, ", ")))
		} else {
			errs = append(errs, fmt.Sprintf("%v: %v\n%v", e.Message, strings.Join(e.Paths, ", "), e.Details))
		}
	}
	return strings.Join(errs, "\n")
}

// Helpers ---

func asIndex(index int) string {
	return fmt.Sprintf("[%d]", index)
}

func isIndex(part string) bool {
	return strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]")
}

func asKey(key string) string {
	return fmt.Sprintf("[%s]", key)
}

// flatten takes in a array of path components and looks for chances to flatten
// objects that have index prefixes, examples:
//   err([0]).ViaField(bar).ViaField(foo) -> foo.bar.[0] converts to foo.bar[0]
//   err(bar).ViaIndex(0).ViaField(foo) -> foo.[0].bar converts to foo[0].bar
//   err(bar).ViaField(foo).ViaIndex(0) -> [0].foo.bar converts to [0].foo.bar
//   err(bar).ViaIndex(0).ViaIndex(1).ViaField(foo) -> foo.[1].[0].bar converts to foo[1][0].bar
func flatten(path []string) string {
	var newPath []string
	for _, part := range path {
		for _, p := range strings.Split(part, ".") {
			if p == CurrentField {
				continue
			} else if len(newPath) > 0 && isIndex(p) {
				newPath[len(newPath)-1] += p
			} else {
				newPath = append(newPath, p)
			}
		}
	}
	return strings.Join(newPath, ".")
}

// mergePaths takes in two string slices and returns the combination of them
// without any duplicate entries.
func mergePaths(a, b []string) []string {
	newPaths := make([]string, 0, len(a)+len(b))
	newPaths = append(newPaths, a...)
	for _, bi := range b {
		if !containsString(newPaths, bi) {
			newPaths = append(newPaths, bi)
		}
	}
	return newPaths
}

// containsString takes in a string slice and looks for the provided string
// within the slice.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// merge takes in a flat list of FieldErrors and returns back a merged list of
// FieldErrors. FieldErrors have their Paths combined (and de-duped) if their
// Message and Details are the same. Merge will not inspect FieldError.errors.
// Merge will also sort the .Path slice, and the errors slice before returning.
func merge(errs []*FieldError) []*FieldError {
	// make a map big enough for all the errors.
	m := make(map[string]*FieldError, len(errs))

	// Convert errs to a map where the key is <message>-<details> and the value
	// is the error. If an error already exists in the map with the same key,
	// then the paths will be merged.
	for _, e := range errs {
		k := key(e)
		if v, ok := m[k]; ok {
			// Found a match, merge the keys.
			v.Paths = mergePaths(v.Paths, e.Paths)
		} else {
			// Does not exist in the map, save the error.
			m[k] = e
		}
	}

	// Take the map made previously and flatten it back out again.
	newErrs := make([]*FieldError, 0, len(m))
	for _, v := range m {
		// While we have access to the merged paths, sort them too.
		sort.Slice(v.Paths, func(i, j int) bool { return v.Paths[i] < v.Paths[j] })
		newErrs = append(newErrs, v)
	}

	// Sort the flattened map.
	sort.Slice(newErrs, func(i, j int) bool {
		if newErrs[i].Message == newErrs[j].Message {
			return newErrs[i].Details < newErrs[j].Details
		}
		return newErrs[i].Message < newErrs[j].Message
	})

	// return back the merged list of sorted errors.
	return newErrs
}

// key returns the key using the fields .Message and .Details.
func key(err *FieldError) string {
	return fmt.Sprintf("%s-%s", err.Message, err.Details)
}

// Public helpers ---

// ErrMissingField is a variadic helper method for constructing a FieldError for
// a set of missing fields.
func ErrMissingField(fieldPaths ...string) *FieldError {
	return &FieldError{
		Message: "missing field(s)",
		Paths:   fieldPaths,
	}
}

// ErrDisallowedFields is a variadic helper method for constructing a FieldError
// for a set of disallowed fields.
func ErrDisallowedFields(fieldPaths ...string) *FieldError {
	return &FieldError{
		Message: "must not set the field(s)",
		Paths:   fieldPaths,
	}
}

// ErrDisallowedUpdateDeprecatedFields is a variadic helper method for
// constructing a FieldError for updating of deprecated fields.
func ErrDisallowedUpdateDeprecatedFields(fieldPaths ...string) *FieldError {
	return &FieldError{
		Message: "must not update deprecated field(s)",
		Paths:   fieldPaths,
	}
}

// ErrInvalidArrayValue constructs a FieldError for a repetetive `field`
// at `index` that has received an invalid string value.
func ErrInvalidArrayValue(value interface{}, field string, index int) *FieldError {
	return ErrInvalidValue(value, CurrentField).ViaFieldIndex(field, index)
}

// ErrInvalidValue constructs a FieldError for a field that has received an
// invalid string value.
func ErrInvalidValue(value interface{}, fieldPath string) *FieldError {
	return &FieldError{
		Message: fmt.Sprintf("invalid value: %v", value),
		Paths:   []string{fieldPath},
	}
}

// ErrMissingOneOf is a variadic helper method for constructing a FieldError for
// not having at least one field in a mutually exclusive field group.
func ErrMissingOneOf(fieldPaths ...string) *FieldError {
	return &FieldError{
		Message: "expected exactly one, got neither",
		Paths:   fieldPaths,
	}
}

// ErrMultipleOneOf is a variadic helper method for constructing a FieldError
// for having more than one field set in a mutually exclusive field group.
func ErrMultipleOneOf(fieldPaths ...string) *FieldError {
	return &FieldError{
		Message: "expected exactly one, got both",
		Paths:   fieldPaths,
	}
}

// ErrInvalidKeyName is a variadic helper method for constructing a FieldError
// that specifies a key name that is invalid.
func ErrInvalidKeyName(key, fieldPath string, details ...string) *FieldError {
	return &FieldError{
		Message: fmt.Sprintf("invalid key name %q", key),
		Paths:   []string{fieldPath},
		Details: strings.Join(details, ", "),
	}
}

// ErrOutOfBoundsValue constructs a FieldError for a field that has received an
// out of bound value.
func ErrOutOfBoundsValue(value, lower, upper interface{}, fieldPath string) *FieldError {
	return &FieldError{
		Message: fmt.Sprintf("expected %v <= %v <= %v", lower, value, upper),
		Paths:   []string{fieldPath},
	}
}

// ErrOutsideLength constructs a FieldError for a field that has received a
// value that it outside of a length range
func ErrOutsideLength(value, lower, upper int, fieldPath string) *FieldError {
	return &FieldError{
		Message: fmt.Sprintf("expected value to be %d-%d characters long, but got length %d", lower, upper, value),
		Paths:   []string{fieldPath},
	}
}

// ErrDuplicate constructs a FieldError for a field value that has been duplicated
// when it should have been unique
func ErrDuplicate(value string, fieldPaths ...string) *FieldError {
	return &FieldError{
		Message: fmt.Sprintf("duplicated value, must be unique: %v", value),
		Paths:   fieldPaths,
	}
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (e *FieldError) DeepCopyInto(out *FieldError) {
	*out = *e
	if e.Paths != nil {
		in, out := &e.Paths, &out.Paths
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if e.errors != nil {
		in, out := &e.errors, &out.errors
		*out = make([]FieldError, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new FieldError.
func (e *FieldError) DeepCopy() *FieldError {
	if e == nil {
		return nil
	}
	out := new(FieldError)
	e.DeepCopyInto(out)
	return out
}
