package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/vamsiramakrishnan/aiplex/pkg/modules"
)

// Module endpoints and pinned descriptors are operator-owned startup config.
// Loading a descriptor never executes downloaded code or silently wires a
// provider capability into an enforcement pipeline.
type remoteModuleSpec struct {
	Descriptor             modules.Descriptor `json:"descriptor"`
	Endpoint               string             `json:"endpoint"`
	AllowInsecureLocalhost bool               `json:"allow_insecure_localhost,omitempty"`
}
type remoteModuleFile struct {
	Modules []remoteModuleSpec `json:"modules"`
}

func registerExternalModules(registry *modules.Registry, path string, development bool) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("module manifest: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("module manifest exceeds 1MiB")
	}
	var spec remoteModuleFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&spec); err != nil {
		return fmt.Errorf("module manifest: %w", err)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing module manifest data")
	}
	for _, entry := range spec.Modules {
		if entry.AllowInsecureLocalhost && !development {
			return fmt.Errorf("module %s requests insecure local transport outside development", entry.Descriptor.Name)
		}
		// These descriptors contribute discovery/lifecycle only. API prefixes are
		// reserved for host-owned adapters; a remote health declaration is not an API.
		if len(entry.Descriptor.APIPrefixes) > 0 {
			return fmt.Errorf("remote module %s requires an explicit host API adapter", entry.Descriptor.Name)
		}
		remote, err := modules.NewRemote(modules.RemoteConfig{Descriptor: entry.Descriptor, Endpoint: entry.Endpoint, AllowInsecureLocalhost: entry.AllowInsecureLocalhost})
		if err != nil {
			return err
		}
		if err = registry.Register(remote); err != nil {
			return err
		}
	}
	return nil
}
