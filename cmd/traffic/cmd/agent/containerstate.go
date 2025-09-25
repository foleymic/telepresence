package agent

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/datawire/dlib/dgroup"
	"github.com/datawire/dlib/dlog"
	"github.com/telepresenceio/telepresence/rpc/v2/manager"
	"github.com/telepresenceio/telepresence/v2/pkg/agentconfig"
	"github.com/telepresenceio/telepresence/v2/pkg/forwarder"
	"github.com/telepresenceio/telepresence/v2/pkg/tunnel"
	"github.com/telepresenceio/telepresence/v2/pkg/types"
)

type containerState struct {
	State
	container  *agentconfig.Container
	mountPoint string
	env        map[string]string
}

func (c *containerState) AddPortHandler(ctx context.Context, pp types.PortAndProto, it agentconfig.InterceptTarget) {
	fwd := c.newPortHandler(pp, it)
	dgroup.ParentGroup(ctx).Go(fmt.Sprintf("forward-%s-%s:%d", c.container.Name, it.Protocol(), it.ContainerPort()), func(ctx context.Context) error {
		return fwd.Serve(tunnel.WithPool(ctx, tunnel.NewPool()), nil)
	})
	c.AddInterceptState(c.NewInterceptState(fwd, it, c.container.Name))
}

func (c *containerState) newPortHandler(pp types.PortAndProto, ics []*agentconfig.Intercept) forwarder.Interceptor {
	ic := ics[0] // They all have the same protocol container port, so the first one will do.
	if c.container.Replace == agentconfig.ReplacePolicyIntercept {
		var tag tunnel.Tag
		var cp uint16
		if ic.TargetPortNumeric {
			// For multi-port services with explicit port mappings, we should use the container port
			// directly instead of the proxy port to avoid port calculation issues.
			// The proxy port calculation (AgentPort + 11 + numberOfPossibleIntercepts) can result
			// in incorrect port mappings when there are multiple intercepts.
			if len(ics) > 1 {
				// Multiple intercepts indicate a multi-port service - use container port directly
				tag = tunnel.AgentToClient
				cp = ic.ContainerPort
			} else {
				// Single intercept - use proxy port for iptables filtering
				tag = tunnel.AgentToProxied
				cp = c.AgentConfig().ProxyPort(ic.ContainerPort)
			}
		} else {
			tag = tunnel.AgentToClient
			cp = ic.ContainerPort
		}
		// Redirect non-intercepted traffic to the pod so that injected sidecars that hijack the ports for
		// incoming connections will continue to work.
		// For containers in the same pod, use localhost instead of pod IP to avoid network connectivity issues
		targetHost := "127.0.0.1"
		return forwarder.NewInterceptor(pp, tag, netip.AddrPortFrom(netip.MustParseAddr(targetHost), cp))
	}
	// The agent will intercept all traffic intended for this container.
	return forwarder.NewInterceptor(pp, tunnel.AgentToClient, netip.AddrPort{})
}

func (c *containerState) GlobalState() State {
	return c.State
}

func (c *containerState) Container() *agentconfig.Container {
	return c.container
}

func (c *containerState) MountPoint() string {
	return c.mountPoint
}

func (c *containerState) Mounts() types.MountPolicies {
	return c.container.Mounts
}

func (c *containerState) Env() map[string]string {
	return c.env
}

func (c *containerState) Name() string {
	return c.container.Name
}

func (c *containerState) ReplaceContainer() bool {
	return c.container.Replace == agentconfig.ReplacePolicyContainer
}

// HandleContainer on the containerState takes care of intercepts that just replaces a container and do not declare
// any ports. Without port declarations, there will be no Intercept entries for an fwdState to handle.
func (c *containerState) HandleContainer(ctx context.Context, iis []*manager.InterceptInfo) (rs []*manager.ReviewInterceptRequest) {
	for _, ii := range iis {
		if ii.Disposition == manager.InterceptDispositionType_WAITING {
			spec := ii.Spec
			if c.ReplaceContainer() && c.Name() == spec.ContainerName && spec.ContainerPort == 0 {
				dlog.Debugf(ctx, "container %s handling replace %s", c.Name(), spec.Name)
				rs = append(rs, &manager.ReviewInterceptRequest{
					Id:          ii.Id,
					Disposition: manager.InterceptDispositionType_ACTIVE,
					PodIp:       c.PodIP().String(),
					SftpPort:    int32(c.SftpPort()),
					FtpPort:     int32(c.FtpPort()),
					MountPoint:  c.MountPoint(),
					Environment: c.Env(),
				})
			}
		}
	}
	return rs
}

// NewContainerState creates a ContainerState that provides the environment variables and the mount point for a container.
func (s *state) NewContainerState(gs State, cn *agentconfig.Container, mountPoint string, env map[string]string) ContainerState {
	return &containerState{
		State:      gs,
		container:  cn,
		mountPoint: mountPoint,
		env:        env,
	}
}
