package localstack

import (
	"context"
	"fmt"
	"github.com/testcontainers/testcontainers-go/wait"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func generateContainerRequest() *LocalStackContainerRequest {
	return &LocalStackContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Env:          map[string]string{},
			ExposedPorts: []string{},
		},
	}
}

func TestConfigureDockerHost(t *testing.T) {
	tests := []struct {
		envVar string
	}{
		{hostnameExternalEnvVar},
		{localstackHostEnvVar},
	}

	for _, tt := range tests {
		t.Run("HOSTNAME_EXTERNAL variable is passed as part of the request", func(t *testing.T) {
			req := generateContainerRequest()

			req.Env[tt.envVar] = "foo"

			reason, err := configureDockerHost(req, tt.envVar)
			assert.Nil(t, err)
			assert.Equal(t, "explicitly as environment variable", reason)
		})

		t.Run("HOSTNAME_EXTERNAL matches the last network alias on a container with non-default network", func(t *testing.T) {
			req := generateContainerRequest()

			req.Networks = []string{"foo", "bar", "baaz"}
			req.NetworkAliases = map[string][]string{
				"foo":  {"foo0", "foo1", "foo2", "foo3"},
				"bar":  {"bar0", "bar1", "bar2", "bar3"},
				"baaz": {"baaz0", "baaz1", "baaz2", "baaz3"},
			}

			reason, err := configureDockerHost(req, tt.envVar)
			assert.Nil(t, err)
			assert.Equal(t, "to match last network alias on container with non-default network", reason)
			assert.Equal(t, "foo3", req.Env[tt.envVar])
		})

		t.Run("HOSTNAME_EXTERNAL matches the daemon host because there are no aliases", func(t *testing.T) {
			dockerProvider, err := testcontainers.NewDockerProvider()
			assert.Nil(t, err)
			defer dockerProvider.Close()

			// because the daemon host could be a remote one, we need to get it from the provider
			expectedDaemonHost, err := dockerProvider.DaemonHost(context.Background())
			assert.Nil(t, err)

			req := generateContainerRequest()

			req.Networks = []string{"foo", "bar", "baaz"}
			req.NetworkAliases = map[string][]string{}

			reason, err := configureDockerHost(req, tt.envVar)
			assert.Nil(t, err)
			assert.Equal(t, "to match host-routable address for container", reason)
			assert.Equal(t, expectedDaemonHost, req.Env[tt.envVar])
		})
	}
}

func TestIsLegacyMode(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"foo", true},
		{"latest", false},
		{"0.10.0", true},
		{"0.10.999", true},
		{"0.11", false},
		{"0.11.2", false},
		{"0.12", false},
		{"1.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := isLegacyMode(fmt.Sprintf("localstack/localstack:%s", tt.version))
			assert.Equal(t, tt.want, got, "runInLegacyMode() = %v, want %v", got, tt.want)
		})
	}
}

func TestStart(t *testing.T) {
	ctx := context.Background()

	// withoutNetwork {
	container, err := StartContainer(
		ctx,
		OverrideContainerRequest(testcontainers.ContainerRequest{
			Image: fmt.Sprintf("localstack/localstack:%s", defaultVersion),
		}),
	)
	// }

	t.Run("multiple services should be exposed using the same port", func(t *testing.T) {
		require.Nil(t, err)
		assert.NotNil(t, container)

		rawPorts, err := container.Ports(ctx)
		require.Nil(t, err)

		ports := 0
		// only one port is exposed among all the ports in the container
		for _, v := range rawPorts {
			if len(v) > 0 {
				ports++
			}
		}

		assert.Equal(t, 1, ports) // a single port is exposed
	})
}

func TestStartWithoutOverride(t *testing.T) {
	// noopOverrideContainerRequest {
	ctx := context.Background()

	container, err := StartContainer(
		ctx,
		NoopOverrideContainerRequest,
	)
	require.Nil(t, err)
	assert.NotNil(t, container)
	// }
}

func TestStartWithNetwork(t *testing.T) {
	// withNetwork {
	ctx := context.Background()

	nw, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{
			Name: "localstack-network",
		},
	})
	require.Nil(t, err)
	assert.NotNil(t, nw)

	container, err := StartContainer(
		ctx,
		OverrideContainerRequest(testcontainers.ContainerRequest{
			Image:          "localstack/localstack:0.13.0",
			Env:            map[string]string{"SERVICES": "s3,sqs"},
			Networks:       []string{"localstack-network"},
			NetworkAliases: map[string][]string{"localstack-network": {"localstack"}},
		}),
	)
	require.Nil(t, err)
	assert.NotNil(t, container)
	// }

	networks, err := container.Networks(ctx)
	require.Nil(t, err)
	require.Equal(t, 1, len(networks))
	require.Equal(t, "localstack-network", networks[0])
}

func TestStartV2WithNetwork(t *testing.T) {
	ctx := context.Background()

	nw, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{
			Name: "localstack-network-v2",
		},
	})
	require.Nil(t, err)
	assert.NotNil(t, nw)

	localstack, err := StartContainer(
		ctx,
		OverrideContainerRequest(testcontainers.ContainerRequest{
			Image:          "localstack/localstack:2.0.0",
			Networks:       []string{"localstack-network-v2"},
			NetworkAliases: map[string][]string{"localstack-network-v2": {"localstack"}},
		}),
	)
	require.Nil(t, err)
	assert.NotNil(t, localstack)

	cli, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:      "amazon/aws-cli:2.7.27",
			Networks:   []string{"localstack-network-v2"},
			Entrypoint: []string{"tail"},
			Cmd:        []string{"-f", "/dev/null"},
			Env: map[string]string{
				"AWS_ACCESS_KEY_ID":     "accesskey",
				"AWS_SECRET_ACCESS_KEY": "secretkey",
				"AWS_REGION":            "eu-west-1",
			},
			WaitingFor: wait.ForExec([]string{"/usr/local/bin/aws", "sqs", "create-queue", "--queue-name", "baz", "--region", "eu-west-1",
				"--endpoint-url", "http://localstack:4566", "--no-verify-ssl"}).
				WithStartupTimeout(time.Second * 10).
				WithExitCodeMatcher(func(exitCode int) bool {
					return exitCode == 0
				}).
				WithResponseMatcher(func(r io.Reader) bool {
					respBytes, _ := io.ReadAll(r)
					resp := string(respBytes)
					return strings.Contains(resp, "http://localstack:4566")
				}),
		},
		Started: true,
	})
	require.Nil(t, err)
	assert.NotNil(t, cli)
}