package tun

import (
	"net"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"testing"

	"golang.org/x/net/ipv4"

	"github.com/telepresenceio/telepresence/v2/pkg/subnet"

	"github.com/datawire/dlib/dlog"

	"github.com/datawire/ambassador/pkg/dtest"
	"github.com/stretchr/testify/suite"
)

func TestTun(t *testing.T) {
	dtest.Sudo()
	dtest.WithMachineLock(func() {
		suite.Run(t, new(tunSuite))
	})
}

type tunSuite struct {
	suite.Suite
	tun *tunDevice
}

func (ts *tunSuite) SetupSuite() {
	require := ts.Require()
	tun, err := OpenTun()
	require.NoError(err, "Failed to open TUN device")
	ts.tun = tun
}

func (ts *tunSuite) TestName() {
	ts.Regexp(regexp.MustCompile(`^utun\d+$`), ts.tun.Name())
}

func (ts *tunSuite) reader() (<-chan []byte, <-chan error) {
	buf := make([]byte, 0x400)
	dataCh := make(chan []byte)
	errCh := make(chan error)
	go func() {
		for {
			n, err := ts.tun.Read(buf)
			if err != nil {
				errCh <- err
			} else {
				dataCh <- buf[4 : n-4 : n-4]
			}
		}
	}()
	return dataCh, errCh
}

func (ts *tunSuite) TestPtP() {
	c := dlog.NewTestContext(ts.T(), true)
	require := ts.Require()
	subnet, err := subnet.FindAvailableClassC()
	require.NoError(err)

	to := make(net.IP, 4)
	copy(to, subnet.IP)
	to[3] = 1

	require.NoError(ts.tun.AddSubnet(c, subnet, to))
	// defer ts.tun.ifconfig(c, "down")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGABRT, syscall.SIGHUP)
	dataChan, errChan := ts.reader()
	for {
		select {
		case buf := <-dataChan:
			// Skip everything but ipv4 UDP requests to dnsIP port 53
			h, err := ipv4.ParseHeader(buf)
			require.NoError(err)
			dlog.Info(c, h)
		case <-sigCh:
			return
		case <-c.Done():
			return
		case err := <-errChan:
			if c.Err() == nil {
				require.NoError(err)
			}
			return
		}
	}
}

func (ts *tunSuite) TearDownSuite() {
	if ts.tun != nil {
		ts.tun.Close()
	}
}
