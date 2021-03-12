package tun

import (
	"io"
	"regexp"
	"testing"

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
	tun  io.ReadWriteCloser
	name string
}

func (ts *tunSuite) SetupSuite() {
	require := ts.Require()
	file, name, err := OpenTun()
	require.NoError(err, "Failed to open TUN device")
	ts.tun = file
	ts.name = name
}

func (ts *tunSuite) TestName() {
	ts.Regexp(regexp.MustCompile(`^utun\d+$`), ts.name)
}

func (ts *tunSuite) TearDownSuite() {
	if ts.tun != nil {
		ts.tun.Close()
	}
}
