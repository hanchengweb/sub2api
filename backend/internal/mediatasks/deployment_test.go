package mediatasks

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVoice3DTestDeploymentDoesNotReuseProductionState(t *testing.T) {
	body, err := os.ReadFile("../../../deploy/docker-compose.voice-3d-test.yml")
	if err != nil {
		t.Fatal(err)
	}
	var stack struct {
		Name     string `yaml:"name"`
		Services map[string]struct {
			Image       string            `yaml:"image"`
			Ports       []string          `yaml:"ports"`
			Environment map[string]string `yaml:"environment"`
			EnvFile     any               `yaml:"env_file"`
			NetworkMode string            `yaml:"network_mode"`
			Volumes     []string          `yaml:"volumes"`
		} `yaml:"services"`
		Volumes map[string]map[string]any `yaml:"volumes"`
	}
	if yaml.Unmarshal(body, &stack) != nil || stack.Name != "windhub-voice3d-test" || len(stack.Services) != 3 {
		t.Fatal("test stack identity")
	}
	gateway := stack.Services["gateway"]
	if len(gateway.Ports) != 1 || !strings.HasPrefix(gateway.Ports[0], "127.0.0.1:") || gateway.Environment["WXM_OFFICIAL_MEDIA_ENABLED"] != "false" || gateway.Environment["DATABASE_DBNAME"] != "windhub_media_test" || gateway.Environment["DATABASE_HOST"] != "postgres" {
		t.Fatal("test binding/database/feature boundary")
	}
	for _, key := range []string{"DATABASE_PASSWORD", "ADMIN_PASSWORD", "JWT_SECRET"} {
		if !strings.HasPrefix(gateway.Environment[key], "${WXM_VOICE3D_TEST_") || !strings.Contains(gateway.Environment[key], ":?") {
			t.Fatal("test-only secret must be required:", key)
		}
	}
	if !strings.HasPrefix(gateway.Image, "${WXM_VOICE3D_TEST_IMAGE:?") {
		t.Fatal("test image must be explicit")
	}
	for _, svc := range stack.Services {
		if svc.EnvFile != nil || svc.NetworkMode != "" {
			t.Fatal("no ambient env_file/host network")
		}
		for _, mount := range svc.Volumes {
			if strings.Contains(mount, "/") && !strings.HasPrefix(mount, "gateway_data:") && !strings.HasPrefix(mount, "postgres_data:") && !strings.HasPrefix(mount, "redis_data:") {
				t.Fatal("unexpected bind/shared mount", mount)
			}
		}
	}
	for name, v := range stack.Volumes {
		if len(v) != 0 {
			t.Fatal("volume must remain project-scoped:", name)
		}
	}
}
