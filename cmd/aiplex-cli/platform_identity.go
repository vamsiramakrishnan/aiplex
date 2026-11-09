package main

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Validate before infrastructure changes: the API deliberately refuses to boot
// without pinned authentication. Shell JWT variables are not Helm values.
func validatePlatformIdentityValues(filename string) error {
	if filename == "" {
		return fmt.Errorf("deployment requires --values with api.env.JWT_ISSUER, JWT_AUDIENCE, and JWT_JWKS_URL, or a mounted AIPLEX_IDENTITY_PROVIDERS_FILE")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read Helm values: %w", err)
	}
	var values struct {
		API struct {
			Env               map[string]any   `yaml:"env"`
			ExtraVolumes      []map[string]any `yaml:"extraVolumes"`
			ExtraVolumeMounts []map[string]any `yaml:"extraVolumeMounts"`
		} `yaml:"api"`
	}
	if err = yaml.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("invalid Helm values")
	}
	if err := validatePlatformConfigMounts(values.API.Env, values.API.ExtraVolumes, values.API.ExtraVolumeMounts); err != nil {
		return err
	}
	get := func(key string) string {
		value, ok := values.API.Env[key].(string)
		if !ok {
			return ""
		}
		return strings.TrimSpace(value)
	}
	if strings.EqualFold(fmt.Sprint(values.API.Env["AIPLEX_DEV_MODE"]), "true") {
		return fmt.Errorf("platform deployment requires configured authentication; loopback development mode cannot serve a deployed platform")
	}
	if get("AIPLEX_IDENTITY_PROVIDERS_FILE") != "" {
		return nil
	}
	issuer, audience, jwks := get("JWT_ISSUER"), get("JWT_AUDIENCE"), get("JWT_JWKS_URL")
	if issuer == "" || audience == "" || jwks == "" {
		return fmt.Errorf("Helm api.env requires JWT_ISSUER, JWT_AUDIENCE, and JWT_JWKS_URL")
	}
	endpoint, err := url.Parse(jwks)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return fmt.Errorf("Helm JWT_JWKS_URL must be a pinned HTTPS endpoint")
	}
	return nil
}

func validatePlatformConfigMounts(env map[string]any, volumes, mounts []map[string]any) error {
	if len(volumes) > 32 || len(mounts) > 64 {
		return fmt.Errorf("too many API configuration mounts")
	}
	names := map[string]bool{}
	for _, volume := range volumes {
		name, _ := volume["name"].(string)
		if name == "" || names[name] {
			return fmt.Errorf("API configuration volumes require unique names")
		}
		names[name] = true
		_, secret := volume["secret"]
		_, configMap := volume["configMap"]
		if secret == configMap {
			return fmt.Errorf("API configuration volumes require exactly one Secret or ConfigMap source")
		}
		for key := range volume {
			if key != "name" && key != "secret" && key != "configMap" {
				return fmt.Errorf("API configuration volumes support only Secret/ConfigMap references")
			}
		}
		key, sourceName := "configMap", "name"
		if secret {
			key, sourceName = "secret", "secretName"
		}
		source, ok := volume[key].(map[string]any)
		ref, validName := source[sourceName].(string)
		if !ok || !validName || strings.TrimSpace(ref) == "" {
			return fmt.Errorf("API configuration source name is required")
		}
	}
	mountPaths := map[string]bool{}
	for _, mount := range mounts {
		name, _ := mount["name"].(string)
		target, _ := mount["mountPath"].(string)
		sub, _ := mount["subPath"].(string)
		if !names[name] || !path.IsAbs(target) || target == "/" || path.Clean(target) != target || strings.ContainsAny(target, "\x00\r\n") || mountPaths[target] {
			return fmt.Errorf("API configuration mounts require a declared source and canonical absolute mountPath")
		}
		mountPaths[target] = true
		if value, exists := mount["readOnly"]; exists && value != true {
			return fmt.Errorf("API configuration mounts must be read-only")
		}
		if sub != "" && (path.IsAbs(sub) || sub == ".." || sub == "." || strings.HasPrefix(sub, "../") || path.Clean(sub) != sub) {
			return fmt.Errorf("API configuration subPath must be canonical and relative")
		}
		for key := range mount {
			if key != "name" && key != "mountPath" && key != "subPath" && key != "readOnly" {
				return fmt.Errorf("unsupported API configuration mount field")
			}
		}
	}
	for _, key := range []string{"AIPLEX_IDENTITY_PROVIDERS_FILE", "AIPLEX_CONNECTIONS_FILE", "AIPLEX_ROLE_BINDINGS_FILE", "AIPLEX_MODULE_MANIFEST", "TLS_CERT_FILE", "TLS_KEY_FILE", "CLIENT_CA_FILE"} {
		filename, ok := env[key].(string)
		if !ok || filename == "" {
			continue
		}
		if !path.IsAbs(filename) || path.Clean(filename) != filename {
			return fmt.Errorf("%s requires a canonical absolute file path", key)
		}
		covered := false
		for _, mount := range mounts {
			target, _ := mount["mountPath"].(string)
			sub, _ := mount["subPath"].(string)
			if sub != "" {
				covered = covered || filename == target
			} else {
				covered = covered || strings.HasPrefix(filename, target+"/")
			}
		}
		if !covered {
			return fmt.Errorf("%s requires a matching read-only Secret/ConfigMap mount", key)
		}
	}
	return nil
}
