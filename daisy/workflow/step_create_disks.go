//  Copyright 2017 Google Inc. All Rights Reserved.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package workflow

import (
	"fmt"
	"strconv"
	"sync"
)

// CreateDisks is a Daisy CreateDisks workflow step.
type CreateDisks []CreateDisk

// CreateDisk describes a GCE disk.
type CreateDisk struct {
	// Name of the disk.
	Name string
	// SourceImage to use during disk creation. Leave blank for a blank
	// disk.
	// See https://godoc.org/google.golang.org/api/compute/v1#Disk.
	SourceImage string `json:",omitempty"`
	// Size of this disk.
	SizeGB string `json:",omitempty"`
	// Type of disk, pd-standard (default) or pd-ssd.
	Type string `json:",omitempty"`
	// Optional description of the resource, if not specified Daisy will
	// create one with the name of the project.
	Description string `json:",omitempty"`
	// Zone to create the instance in, overrides workflow Zone.
	Zone string `json:",omitempty"`
	// Project to create the instance in, overrides workflow Project.
	Project string `json:",omitempty"`
	// Should this resource be cleaned up after the workflow?
	NoCleanup bool
	// Should we use the user-provided reference name as the actual
	// resource name?
	ExactName bool
}

func (c *CreateDisks) validate(s *Step) error {
	w := s.w
	for _, cd := range *c {
		// Image checking.
		if cd.SourceImage != "" && !imageValid(w, cd.SourceImage) {
			return fmt.Errorf("cannot create disk: image not found: %s", cd.SourceImage)
		}

		if _, err := strconv.ParseInt(cd.SizeGB, 10, 64); cd.SizeGB != "" && err != nil {
			return fmt.Errorf("cannot parse SizeGB: %s, err: %v", cd.SizeGB, err)
		}

		// No SizeGB set when not supplying SourceImage.
		if cd.SizeGB == "" && cd.SourceImage == "" {
			return fmt.Errorf("cannot create disk: SizeGB and SourceImage not set: %s", cd.SourceImage)
		}

		// Try adding disk name.
		if err := validatedDisks.add(w, cd.Name); err != nil {
			return fmt.Errorf("error adding disk: %s", err)
		}
	}

	return nil
}

func (c *CreateDisks) run(s *Step) error {
	var wg sync.WaitGroup
	w := s.w
	e := make(chan error)
	for _, cd := range *c {
		wg.Add(1)
		go func(cd CreateDisk) {
			defer wg.Done()
			name := cd.Name
			if !cd.ExactName {
				name = w.genName(cd.Name)
			}

			zone := w.Zone
			if cd.Zone != "" {
				zone = cd.Zone
			}

			project := w.Project
			if cd.Project != "" {
				project = cd.Project
			}

			// Get the source image link.
			var imageLink string
			if cd.SourceImage == "" || imageURLRegex.MatchString(cd.SourceImage) {
				imageLink = cd.SourceImage
			} else if image, ok := images[w].get(cd.SourceImage); ok {
				imageLink = image.link
			} else {
				e <- fmt.Errorf("invalid or missing reference to SourceImage %q", cd.SourceImage)
				return
			}

			size, err := strconv.ParseInt(cd.SizeGB, 10, 64)
			if cd.SizeGB != "" && err != nil {
				e <- err
				return
			}

			w.logger.Printf("CreateDisks: creating disk %q.", name)
			description := cd.Description
			if description == "" {
				description = fmt.Sprintf("Disk created by Daisy in workflow %q on behalf of %s.", w.Name, w.username)
			}
			d, err := w.ComputeClient.CreateDisk(name, project, zone, imageLink, size, cd.Type, description)
			if err != nil {
				e <- err
				return
			}
			disks[w].add(cd.Name, &resource{cd.Name, name, d.SelfLink, cd.NoCleanup, false})
		}(cd)
	}

	go func() {
		wg.Wait()
		e <- nil
	}()

	select {
	case err := <-e:
		return err
	case <-w.Cancel:
		// Wait so disks being created now can be deleted.
		wg.Wait()
		return nil
	}
}
