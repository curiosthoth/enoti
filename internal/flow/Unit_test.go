package flow

import (
	"enoti/internal/ports"
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	AWSMockPort    = 4566
	TestServerPort = 39080
	TestTableName  = "notify_guard_test-clients"
)

var wg sync.WaitGroup

type UnitTestSuite struct {
	suite.Suite

	clientStore ports.ClientStore
	stopChan    chan<- struct{} // Send only
	doneChan    <-chan error    // Receive only
}

func TestUnitTestSuite(t *testing.T) {
	suite.Run(t, new(UnitTestSuite))
}
