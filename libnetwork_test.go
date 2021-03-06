package libnetwork_test

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/plugins"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/libnetwork"
	"github.com/docker/libnetwork/datastore"
	"github.com/docker/libnetwork/driverapi"
	"github.com/docker/libnetwork/netlabel"
	"github.com/docker/libnetwork/netutils"
	"github.com/docker/libnetwork/options"
	"github.com/docker/libnetwork/osl"
	"github.com/docker/libnetwork/types"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	bridgeNetType = "bridge"
)

var controller libnetwork.NetworkController

func TestMain(m *testing.M) {
	if reexec.Init() {
		return
	}

	if err := createController(); err != nil {
		os.Exit(1)
	}
	option := options.Generic{
		"EnableIPForwarding": true,
	}

	genericOption := make(map[string]interface{})
	genericOption[netlabel.GenericData] = option

	err := controller.ConfigureNetworkDriver(bridgeNetType, genericOption)
	if err != nil {
		//m.Fatal(err)
		os.Exit(1)
	}

	libnetwork.SetTestDataStore(controller, datastore.NewCustomDataStore(datastore.NewMockStore()))

	os.Exit(m.Run())
}

func createController() error {
	var err error

	controller, err = libnetwork.New()
	if err != nil {
		return err
	}

	return nil
}

func createTestNetwork(networkType, networkName string, netOption options.Generic) (libnetwork.Network, error) {
	network, err := controller.NewNetwork(networkType, networkName,
		libnetwork.NetworkOptionGeneric(netOption))
	if err != nil {
		return nil, err
	}

	return network, nil
}

func getEmptyGenericOption() map[string]interface{} {
	genericOption := make(map[string]interface{})
	genericOption[netlabel.GenericData] = options.Generic{}
	return genericOption
}

func getPortMapping() []types.PortBinding {
	return []types.PortBinding{
		{Proto: types.TCP, Port: uint16(230), HostPort: uint16(23000)},
		{Proto: types.UDP, Port: uint16(200), HostPort: uint16(22000)},
		{Proto: types.TCP, Port: uint16(120), HostPort: uint16(12000)},
		{Proto: types.TCP, Port: uint16(320), HostPort: uint16(32000), HostPortEnd: uint16(32999)},
		{Proto: types.UDP, Port: uint16(420), HostPort: uint16(42000), HostPortEnd: uint16(42001)},
	}
}

func TestNull(t *testing.T) {
	cnt, err := controller.NewSandbox("null_container",
		libnetwork.OptionHostname("test"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"))
	if err != nil {
		t.Fatal(err)
	}

	network, err := createTestNetwork("null", "testnull", options.Generic{})
	if err != nil {
		t.Fatal(err)
	}

	ep, err := network.CreateEndpoint("testep")
	if err != nil {
		t.Fatal(err)
	}

	err = ep.Join(cnt)
	if err != nil {
		t.Fatal(err)
	}

	err = ep.Leave(cnt)
	if err != nil {
		t.Fatal(err)
	}

	if err := ep.Delete(); err != nil {
		t.Fatal(err)
	}

	if err := cnt.Delete(); err != nil {
		t.Fatal(err)
	}

	// host type is special network. Cannot be removed.
	err = network.Delete()
	if err == nil {
		t.Fatal(err)
	}
	if _, ok := err.(types.ForbiddenError); !ok {
		t.Fatalf("Unexpected error type")
	}
}

func TestHost(t *testing.T) {
	sbx1, err := controller.NewSandbox("host_c1",
		libnetwork.OptionHostname("test1"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"),
		libnetwork.OptionUseDefaultSandbox())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sbx1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	sbx2, err := controller.NewSandbox("host_c2",
		libnetwork.OptionHostname("test2"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"),
		libnetwork.OptionUseDefaultSandbox())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sbx2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	network, err := createTestNetwork("host", "testhost", options.Generic{})
	if err != nil {
		t.Fatal(err)
	}

	ep1, err := network.CreateEndpoint("testep1")
	if err != nil {
		t.Fatal(err)
	}

	if err := ep1.Join(sbx1); err != nil {
		t.Fatal(err)
	}

	ep2, err := network.CreateEndpoint("testep2")
	if err != nil {
		t.Fatal(err)
	}

	if err := ep2.Join(sbx2); err != nil {
		t.Fatal(err)
	}

	if err := ep1.Leave(sbx1); err != nil {
		t.Fatal(err)
	}

	if err := ep2.Leave(sbx2); err != nil {
		t.Fatal(err)
	}

	if err := ep1.Delete(); err != nil {
		t.Fatal(err)
	}

	if err := ep2.Delete(); err != nil {
		t.Fatal(err)
	}

	// Try to create another host endpoint and join/leave that.
	cnt3, err := controller.NewSandbox("host_c3",
		libnetwork.OptionHostname("test3"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"),
		libnetwork.OptionUseDefaultSandbox())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cnt3.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep3, err := network.CreateEndpoint("testep3")
	if err != nil {
		t.Fatal(err)
	}

	if err := ep3.Join(sbx2); err != nil {
		t.Fatal(err)
	}

	if err := ep3.Leave(sbx2); err != nil {
		t.Fatal(err)
	}

	if err := ep3.Delete(); err != nil {
		t.Fatal(err)
	}

	// host type is special network. Cannot be removed.
	err = network.Delete()
	if err == nil {
		t.Fatal(err)
	}
	if _, ok := err.(types.ForbiddenError); !ok {
		t.Fatalf("Unexpected error type")
	}
}

func TestBridge(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	ip, subnet, err := net.ParseCIDR("192.168.100.1/24")
	if err != nil {
		t.Fatal(err)
	}
	subnet.IP = ip

	ip, cidr, err := net.ParseCIDR("192.168.100.2/28")
	if err != nil {
		t.Fatal(err)
	}
	cidr.IP = ip

	ip, cidrv6, err := net.ParseCIDR("fe90::1/96")
	if err != nil {
		t.Fatal(err)
	}
	cidrv6.IP = ip

	log.Debug("Adding a bridge")

	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AddressIPv4":           subnet,
			"FixedCIDR":             cidr,
			"FixedCIDRv6":           cidrv6,
			"EnableIPv6":            true,
			"EnableICC":             true,
			"AllowNonDefaultBridge": true,
			"EnableIPMasquerade":    true,
		},
	}

	network, err := createTestNetwork(bridgeNetType, "testnetwork", netOption)
	if err != nil {
		t.Fatal(err)
	}

	ep, err := network.CreateEndpoint("testep", libnetwork.CreateOptionPortMapping(getPortMapping()))
	if err != nil {
		t.Fatal(err)
	}

	epInfo, err := ep.DriverInfo()
	if err != nil {
		t.Fatal(err)
	}
	pmd, ok := epInfo[netlabel.PortMap]
	if !ok {
		t.Fatalf("Could not find expected info in endpoint data")
	}
	pm, ok := pmd.([]types.PortBinding)
	if !ok {
		t.Fatalf("Unexpected format for port mapping in endpoint operational data")
	}
	if len(pm) != 5 {
		t.Fatalf("Incomplete data for port mapping in endpoint operational data: %d", len(pm))
	}

	if err := ep.Delete(); err != nil {
		t.Fatal(err)
	}

	if err := network.Delete(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownDriver(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	_, err := createTestNetwork("unknowndriver", "testnetwork", options.Generic{})
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if _, ok := err.(types.NotFoundError); !ok {
		t.Fatalf("Did not fail with expected error. Actual error: %v", err)
	}
}

func TestNilRemoteDriver(t *testing.T) {
	_, err := controller.NewNetwork("framerelay", "dummy",
		libnetwork.NetworkOptionGeneric(getEmptyGenericOption()))
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if _, ok := err.(types.NotFoundError); !ok {
		t.Fatalf("Did not fail with expected error. Actual error: %v", err)
	}
}

func TestDuplicateNetwork(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	// Creating a default bridge name network (can't be removed)
	_, err := controller.NewNetwork(bridgeNetType, "testdup")
	if err != nil {
		t.Fatal(err)
	}

	_, err = controller.NewNetwork(bridgeNetType, "testdup")
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if _, ok := err.(libnetwork.NetworkNameError); !ok {
		t.Fatalf("Did not fail with expected error. Actual error: %v", err)
	}
}

func TestNetworkName(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	}

	_, err := createTestNetwork(bridgeNetType, "", netOption)
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if _, ok := err.(libnetwork.ErrInvalidName); !ok {
		t.Fatalf("Expected to fail with ErrInvalidName error. Got %v", err)
	}

	networkName := "testnetwork"
	n, err := createTestNetwork(bridgeNetType, networkName, netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	if n.Name() != networkName {
		t.Fatalf("Expected network name %s, got %s", networkName, n.Name())
	}
}

func TestNetworkType(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	}

	n, err := createTestNetwork(bridgeNetType, "testnetwork", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	if n.Type() != bridgeNetType {
		t.Fatalf("Expected network type %s, got %s", bridgeNetType, n.Type())
	}
}

func TestNetworkID(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	}

	n, err := createTestNetwork(bridgeNetType, "testnetwork", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	if n.ID() == "" {
		t.Fatal("Expected non-empty network id")
	}
}

func TestDeleteNetworkWithActiveEndpoints(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	netOption := options.Generic{
		"BridgeName":            "testnetwork",
		"AllowNonDefaultBridge": true}
	option := options.Generic{
		netlabel.GenericData: netOption,
	}

	network, err := createTestNetwork(bridgeNetType, "testnetwork", option)
	if err != nil {
		t.Fatal(err)
	}

	ep, err := network.CreateEndpoint("testep")
	if err != nil {
		t.Fatal(err)
	}

	err = network.Delete()
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if _, ok := err.(*libnetwork.ActiveEndpointsError); !ok {
		t.Fatalf("Did not fail with expected error. Actual error: %v", err)
	}

	// Done testing. Now cleanup.
	if err := ep.Delete(); err != nil {
		t.Fatal(err)
	}

	if err := network.Delete(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownNetwork(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	netOption := options.Generic{
		"BridgeName":            "testnetwork",
		"AllowNonDefaultBridge": true}
	option := options.Generic{
		netlabel.GenericData: netOption,
	}

	network, err := createTestNetwork(bridgeNetType, "testnetwork", option)
	if err != nil {
		t.Fatal(err)
	}

	err = network.Delete()
	if err != nil {
		t.Fatal(err)
	}

	err = network.Delete()
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if _, ok := err.(*libnetwork.UnknownNetworkError); !ok {
		t.Fatalf("Did not fail with expected error. Actual error: %v", err)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	ip, subnet, err := net.ParseCIDR("192.168.100.1/24")
	if err != nil {
		t.Fatal(err)
	}
	subnet.IP = ip

	netOption := options.Generic{
		"BridgeName":            "testnetwork",
		"AddressIPv4":           subnet,
		"AllowNonDefaultBridge": true}
	option := options.Generic{
		netlabel.GenericData: netOption,
	}

	network, err := createTestNetwork(bridgeNetType, "testnetwork", option)
	if err != nil {
		t.Fatal(err)
	}

	_, err = network.CreateEndpoint("")
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}
	if _, ok := err.(libnetwork.ErrInvalidName); !ok {
		t.Fatalf("Expected to fail with ErrInvalidName error. Actual error: %v", err)
	}

	ep, err := network.CreateEndpoint("testep")
	if err != nil {
		t.Fatal(err)
	}

	err = ep.Delete()
	if err != nil {
		t.Fatal(err)
	}

	// Done testing. Now cleanup
	if err := network.Delete(); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkEndpointsWalkers(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	// Create network 1 and add 2 endpoint: ep11, ep12
	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "network1",
			"AllowNonDefaultBridge": true,
		},
	}

	net1, err := createTestNetwork(bridgeNetType, "network1", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := net1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep11, err := net1.CreateEndpoint("ep11")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep11.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep12, err := net1.CreateEndpoint("ep12")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep12.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	// Test list methods on net1
	epList1 := net1.Endpoints()
	if len(epList1) != 2 {
		t.Fatalf("Endpoints() returned wrong number of elements: %d instead of 2", len(epList1))
	}
	// endpoint order is not guaranteed
	for _, e := range epList1 {
		if e != ep11 && e != ep12 {
			t.Fatal("Endpoints() did not return all the expected elements")
		}
	}

	// Test Endpoint Walk method
	var epName string
	var epWanted libnetwork.Endpoint
	wlk := func(ep libnetwork.Endpoint) bool {
		if ep.Name() == epName {
			epWanted = ep
			return true
		}
		return false
	}

	// Look for ep1 on network1
	epName = "ep11"
	net1.WalkEndpoints(wlk)
	if epWanted == nil {
		t.Fatal(err)
	}
	if ep11 != epWanted {
		t.Fatal(err)
	}

	current := len(controller.Networks())

	// Create network 2
	netOption = options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "network2",
			"AllowNonDefaultBridge": true,
		},
	}

	net2, err := createTestNetwork(bridgeNetType, "network2", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := net2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	// Test Networks method
	if len(controller.Networks()) != current+1 {
		t.Fatalf("Did not find the expected number of networks")
	}

	// Test Network Walk method
	var netName string
	var netWanted libnetwork.Network
	nwWlk := func(nw libnetwork.Network) bool {
		if nw.Name() == netName {
			netWanted = nw
			return true
		}
		return false
	}

	// Look for network named "network1" and "network2"
	netName = "network1"
	controller.WalkNetworks(nwWlk)
	if netWanted == nil {
		t.Fatal(err)
	}
	if net1 != netWanted {
		t.Fatal(err)
	}

	netName = "network2"
	controller.WalkNetworks(nwWlk)
	if netWanted == nil {
		t.Fatal(err)
	}
	if net2 != netWanted {
		t.Fatal(err)
	}
}

func TestDuplicateEndpoint(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	}
	n, err := createTestNetwork(bridgeNetType, "testnetwork", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep, err := n.CreateEndpoint("ep1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep2, err := n.CreateEndpoint("ep1")
	defer func() {
		// Cleanup ep2 as well, else network cleanup might fail for failure cases
		if ep2 != nil {
			if err := ep2.Delete(); err != nil {
				t.Fatal(err)
			}
		}
	}()

	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if _, ok := err.(types.ForbiddenError); !ok {
		t.Fatalf("Did not fail with expected error. Actual error: %v", err)
	}
}

func TestControllerQuery(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	// Create network 1
	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "network1",
			"AllowNonDefaultBridge": true,
		},
	}
	net1, err := createTestNetwork(bridgeNetType, "network1", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := net1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create network 2
	netOption = options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "network2",
			"AllowNonDefaultBridge": true,
		},
	}
	net2, err := createTestNetwork(bridgeNetType, "network2", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := net2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	_, err = controller.NetworkByName("")
	if err == nil {
		t.Fatalf("NetworkByName() succeeded with invalid target name")
	}
	if _, ok := err.(libnetwork.ErrInvalidName); !ok {
		t.Fatalf("Expected NetworkByName() to fail with ErrInvalidName error. Got: %v", err)
	}

	_, err = controller.NetworkByID("")
	if err == nil {
		t.Fatalf("NetworkByID() succeeded with invalid target id")
	}
	if _, ok := err.(libnetwork.ErrInvalidID); !ok {
		t.Fatalf("NetworkByID() failed with unexpected error: %v", err)
	}

	g, err := controller.NetworkByID("network1")
	if err == nil {
		t.Fatalf("Unexpected success for NetworkByID(): %v", g)
	}
	if _, ok := err.(libnetwork.ErrNoSuchNetwork); !ok {
		t.Fatalf("NetworkByID() failed with unexpected error: %v", err)
	}

	g, err = controller.NetworkByName("network1")
	if err != nil {
		t.Fatalf("Unexpected failure for NetworkByName(): %v", err)
	}
	if g == nil {
		t.Fatalf("NetworkByName() did not find the network")
	}

	if g != net1 {
		t.Fatalf("NetworkByName() returned the wrong network")
	}

	g, err = controller.NetworkByID(net1.ID())
	if err != nil {
		t.Fatalf("Unexpected failure for NetworkByID(): %v", err)
	}
	if net1 != g {
		t.Fatalf("NetworkByID() returned unexpected element: %v", g)
	}

	g, err = controller.NetworkByName("network2")
	if err != nil {
		t.Fatalf("Unexpected failure for NetworkByName(): %v", err)
	}
	if g == nil {
		t.Fatalf("NetworkByName() did not find the network")
	}

	if g != net2 {
		t.Fatalf("NetworkByName() returned the wrong network")
	}

	g, err = controller.NetworkByID(net2.ID())
	if err != nil {
		t.Fatalf("Unexpected failure for NetworkByID(): %v", err)
	}
	if net2 != g {
		t.Fatalf("NetworkByID() returned unexpected element: %v", g)
	}
}

func TestNetworkQuery(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	// Create network 1 and add 2 endpoint: ep11, ep12
	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "network1",
			"AllowNonDefaultBridge": true,
		},
	}
	net1, err := createTestNetwork(bridgeNetType, "network1", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := net1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep11, err := net1.CreateEndpoint("ep11")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep11.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep12, err := net1.CreateEndpoint("ep12")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep12.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	e, err := net1.EndpointByName("ep11")
	if err != nil {
		t.Fatal(err)
	}
	if ep11 != e {
		t.Fatalf("EndpointByName() returned %v instead of %v", e, ep11)
	}

	e, err = net1.EndpointByName("")
	if err == nil {
		t.Fatalf("EndpointByName() succeeded with invalid target name")
	}
	if _, ok := err.(libnetwork.ErrInvalidName); !ok {
		t.Fatalf("Expected EndpointByName() to fail with ErrInvalidName error. Got: %v", err)
	}

	e, err = net1.EndpointByName("IamNotAnEndpoint")
	if err == nil {
		t.Fatalf("EndpointByName() succeeded with unknown target name")
	}
	if _, ok := err.(libnetwork.ErrNoSuchEndpoint); !ok {
		t.Fatal(err)
	}
	if e != nil {
		t.Fatalf("EndpointByName(): expected nil, got %v", e)
	}

	e, err = net1.EndpointByID(ep12.ID())
	if err != nil {
		t.Fatal(err)
	}
	if ep12 != e {
		t.Fatalf("EndpointByID() returned %v instead of %v", e, ep12)
	}

	e, err = net1.EndpointByID("")
	if err == nil {
		t.Fatalf("EndpointByID() succeeded with invalid target id")
	}
	if _, ok := err.(libnetwork.ErrInvalidID); !ok {
		t.Fatalf("EndpointByID() failed with unexpected error: %v", err)
	}
}

const containerID = "valid_c"

func checkSandbox(t *testing.T, info libnetwork.EndpointInfo) {
	origns, err := netns.Get()
	if err != nil {
		t.Fatalf("Could not get the current netns: %v", err)
	}
	defer origns.Close()

	key := info.Sandbox().Key()
	f, err := os.OpenFile(key, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("Failed to open network namespace path %q: %v", key, err)
	}
	defer f.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	nsFD := f.Fd()
	if err = netns.Set(netns.NsHandle(nsFD)); err != nil {
		t.Fatalf("Setting to the namespace pointed to by the sandbox %s failed: %v", key, err)
	}
	defer netns.Set(origns)

	_, err = netlink.LinkByName("eth0")
	if err != nil {
		t.Fatalf("Could not find the interface eth0 inside the sandbox: %v", err)
	}

	_, err = netlink.LinkByName("eth1")
	if err != nil {
		t.Fatalf("Could not find the interface eth1 inside the sandbox: %v", err)
	}
}

func TestEndpointJoin(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	// Create network 1 and add 2 endpoint: ep11, ep12
	n1, err := createTestNetwork(bridgeNetType, "testnetwork1", options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork1",
			"AllowNonDefaultBridge": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep1, err := n1.CreateEndpoint("ep1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	// Validate if ep.Info() only gives me IP address info and not names and gateway during CreateEndpoint()
	info := ep1.Info()
	for _, iface := range info.InterfaceList() {
		if iface.Address().IP.To4() == nil {
			t.Fatalf("Invalid IP address returned: %v", iface.Address())
		}
	}

	if info.Gateway().To4() != nil {
		t.Fatalf("Expected empty gateway for an empty endpoint. Instead found a gateway: %v", info.Gateway())
	}

	if info.Sandbox() != nil {
		t.Fatalf("Expected an empty sandbox key for an empty endpoint. Instead found a non-empty sandbox key: %s", info.Sandbox().Key())
	}

	// test invalid joins
	err = ep1.Join(nil)
	if err == nil {
		t.Fatalf("Expected to fail join with nil Sandbox")
	}
	if _, ok := err.(types.BadRequestError); !ok {
		t.Fatalf("Unexpected error type returned: %T", err)
	}

	fsbx := &fakeSandbox{}
	if err = ep1.Join(fsbx); err == nil {
		t.Fatalf("Expected to fail join with invalid Sandbox")
	}
	if _, ok := err.(types.BadRequestError); !ok {
		t.Fatalf("Unexpected error type returned: %T", err)
	}

	sb, err := controller.NewSandbox(containerID,
		libnetwork.OptionHostname("test"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := sb.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep1.Join(sb)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = ep1.Leave(sb)
		runtime.LockOSThread()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Validate if ep.Info() only gives valid gateway and sandbox key after has container has joined.
	info = ep1.Info()
	if info.Gateway().To4() == nil {
		t.Fatalf("Expected a valid gateway for a joined endpoint. Instead found an invalid gateway: %v", info.Gateway())
	}

	if info.Sandbox() == nil {
		t.Fatalf("Expected an non-empty sandbox key for a joined endpoint. Instead found a empty sandbox key")
	}

	// Check endpoint provided container information
	if ep1.Info().Sandbox().Key() != sb.Key() {
		t.Fatalf("Endpoint Info returned unexpected sandbox key: %s", sb.Key())
	}

	// Attempt retrieval of endpoint interfaces statistics
	stats, err := sb.Statistics()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stats["eth0"]; !ok {
		t.Fatalf("Did not find eth0 statistics")
	}

	// Now test the container joining another network
	n2, err := createTestNetwork(bridgeNetType, "testnetwork2",
		options.Generic{
			netlabel.GenericData: options.Generic{
				"BridgeName":            "testnetwork2",
				"AllowNonDefaultBridge": true,
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep2, err := n2.CreateEndpoint("ep2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep2.Join(sb)
	if err != nil {
		t.Fatal(err)
	}
	runtime.LockOSThread()
	defer func() {
		err = ep2.Leave(sb)
		runtime.LockOSThread()
		if err != nil {
			t.Fatal(err)
		}
	}()

	if ep1.Info().Sandbox().Key() != ep2.Info().Sandbox().Key() {
		t.Fatalf("ep1 and ep2 returned different container sandbox key")
	}

	checkSandbox(t, info)
}

type fakeSandbox struct{}

func (f *fakeSandbox) ID() string {
	return "fake sandbox"
}

func (f *fakeSandbox) ContainerID() string {
	return ""
}

func (f *fakeSandbox) Key() string {
	return "fake key"
}

func (f *fakeSandbox) Labels() map[string]interface{} {
	return nil
}

func (f *fakeSandbox) Statistics() (map[string]*osl.InterfaceStatistics, error) {
	return nil, nil
}

func (f *fakeSandbox) Delete() error {
	return nil
}

func TestEndpointDeleteWithActiveContainer(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	n, err := createTestNetwork(bridgeNetType, "testnetwork", options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep, err := n.CreateEndpoint("ep1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = ep.Delete()
		if err != nil {
			t.Fatal(err)
		}
	}()

	cnt, err := controller.NewSandbox(containerID,
		libnetwork.OptionHostname("test"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"))
	defer func() {
		if err := cnt.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep.Join(cnt)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = ep.Leave(cnt)
		runtime.LockOSThread()
		if err != nil {
			t.Fatal(err)
		}
	}()

	err = ep.Delete()
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if _, ok := err.(*libnetwork.ActiveContainerError); !ok {
		t.Fatalf("Did not fail with expected error. Actual error: %v", err)
	}
}

func TestEndpointMultipleJoins(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	n, err := createTestNetwork(bridgeNetType, "testmultiple", options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testmultiple",
			"AllowNonDefaultBridge": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep, err := n.CreateEndpoint("ep1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	sbx1, err := controller.NewSandbox(containerID,
		libnetwork.OptionHostname("test"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"))
	defer func() {
		if err := sbx1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	sbx2, err := controller.NewSandbox("c2")
	defer func() {
		if err := sbx2.Delete(); err != nil {
			t.Fatal(err)
		}
		runtime.LockOSThread()
	}()

	err = ep.Join(sbx1)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = ep.Leave(sbx1)
		runtime.LockOSThread()
		if err != nil {
			t.Fatal(err)
		}
	}()

	err = ep.Join(sbx2)
	if err == nil {
		t.Fatal("Expected to fail multiple joins for the same endpoint")
	}

	if _, ok := err.(types.ForbiddenError); !ok {
		t.Fatalf("Failed with unexpected error type: %T. Desc: %s", err, err.Error())
	}

}

func TestLeaveAll(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	n, err := createTestNetwork(bridgeNetType, "testnetwork", options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// If this goes through, it means cnt.Delete() effectively detached from all the endpoints
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep1, err := n.CreateEndpoint("ep1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep2, err := n.CreateEndpoint("ep2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	cnt, err := controller.NewSandbox("leaveall")
	if err != nil {
		t.Fatal(err)
	}

	err = ep1.Join(cnt)
	if err != nil {
		t.Fatalf("Failed to join ep1: %v", err)
	}
	runtime.LockOSThread()

	err = ep2.Join(cnt)
	if err != nil {
		t.Fatalf("Failed to join ep2: %v", err)
	}
	runtime.LockOSThread()

	err = cnt.Delete()
	if err != nil {
		t.Fatal(err)
	}
}

func TestontainerInvalidLeave(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	n, err := createTestNetwork(bridgeNetType, "testnetwork", options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep, err := n.CreateEndpoint("ep1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	cnt, err := controller.NewSandbox(containerID,
		libnetwork.OptionHostname("test"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cnt.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep.Leave(cnt)
	if err == nil {
		t.Fatal("Expected to fail leave from an endpoint which has no active join")
	}
	if _, ok := err.(types.ForbiddenError); !ok {
		t.Fatalf("Failed with unexpected error type: %T. Desc: %s", err, err.Error())
	}

	if err := ep.Leave(nil); err == nil {
		t.Fatalf("Expected to fail leave nil Sandbox")
	}
	if _, ok := err.(types.BadRequestError); !ok {
		t.Fatalf("Unexpected error type returned: %T", err)
	}

	fsbx := &fakeSandbox{}
	if err = ep.Leave(fsbx); err == nil {
		t.Fatalf("Expected to fail leave with invalid Sandbox")
	}
	if _, ok := err.(types.BadRequestError); !ok {
		t.Fatalf("Unexpected error type returned: %T", err)
	}
}

func TestEndpointUpdateParent(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	n, err := createTestNetwork("bridge", "testnetwork", options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep1, err := n.CreateEndpoint("ep1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep2, err := n.CreateEndpoint("ep2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	sbx1, err := controller.NewSandbox(containerID,
		libnetwork.OptionHostname("test"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionExtraHost("web", "192.168.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sbx1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	sbx2, err := controller.NewSandbox("c2",
		libnetwork.OptionHostname("test2"),
		libnetwork.OptionDomainname("docker.io"),
		libnetwork.OptionHostsPath("/var/lib/docker/test_network/container2/hosts"),
		libnetwork.OptionExtraHost("web", "192.168.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sbx2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep1.Join(sbx1)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}

	err = ep2.Join(sbx2)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}

	err = ep2.Leave(sbx2)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnableIPv6(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	tmpResolvConf := []byte("search pommesfrites.fr\nnameserver 12.34.56.78\nnameserver 2001:4860:4860::8888\n")
	//take a copy of resolv.conf for restoring after test completes
	resolvConfSystem, err := ioutil.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	//cleanup
	defer func() {
		if err := ioutil.WriteFile("/etc/resolv.conf", resolvConfSystem, 0644); err != nil {
			t.Fatal(err)
		}
	}()

	ip, cidrv6, err := net.ParseCIDR("fe80::1/64")
	if err != nil {
		t.Fatal(err)
	}
	cidrv6.IP = ip

	netOption := options.Generic{
		netlabel.EnableIPv6: true,
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"FixedCIDRv6":           cidrv6,
			"AllowNonDefaultBridge": true,
		},
	}

	n, err := createTestNetwork("bridge", "testnetwork", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep1, err := n.CreateEndpoint("ep1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := ioutil.WriteFile("/etc/resolv.conf", tmpResolvConf, 0644); err != nil {
		t.Fatal(err)
	}

	resolvConfPath := "/tmp/libnetwork_test/resolv.conf"
	defer os.Remove(resolvConfPath)

	sb, err := controller.NewSandbox(containerID, libnetwork.OptionResolvConfPath(resolvConfPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sb.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep1.Join(sb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = ep1.Leave(sb)
		runtime.LockOSThread()
		if err != nil {
			t.Fatal(err)
		}
	}()

	content, err := ioutil.ReadFile(resolvConfPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(content, tmpResolvConf) {
		t.Fatalf("Expected:\n%s\nGot:\n%s", string(tmpResolvConf), string(content))
	}

	if err != nil {
		t.Fatal(err)
	}
}

func TestResolvConfHost(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	tmpResolvConf := []byte("search localhost.net\nnameserver 127.0.0.1\nnameserver 2001:4860:4860::8888\n")

	//take a copy of resolv.conf for restoring after test completes
	resolvConfSystem, err := ioutil.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	//cleanup
	defer func() {
		if err := ioutil.WriteFile("/etc/resolv.conf", resolvConfSystem, 0644); err != nil {
			t.Fatal(err)
		}
	}()

	n, err := controller.NetworkByName("testhost")
	if err != nil {
		t.Fatal(err)
	}

	ep1, err := n.CreateEndpoint("ep1", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := ioutil.WriteFile("/etc/resolv.conf", tmpResolvConf, 0644); err != nil {
		t.Fatal(err)
	}

	resolvConfPath := "/tmp/libnetwork_test/resolv.conf"
	defer os.Remove(resolvConfPath)

	sb, err := controller.NewSandbox(containerID,
		libnetwork.OptionResolvConfPath(resolvConfPath),
		libnetwork.OptionOriginResolvConfPath("/etc/resolv.conf"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sb.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep1.Join(sb)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = ep1.Leave(sb)
		if err != nil {
			t.Fatal(err)
		}
	}()

	finfo, err := os.Stat(resolvConfPath)
	if err != nil {
		t.Fatal(err)
	}

	fmode := (os.FileMode)(0644)
	if finfo.Mode() != fmode {
		t.Fatalf("Expected file mode %s, got %s", fmode.String(), finfo.Mode().String())
	}

	content, err := ioutil.ReadFile(resolvConfPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(content, tmpResolvConf) {
		t.Fatalf("Expected:\n%s\nGot:\n%s", string(tmpResolvConf), string(content))
	}
}

func TestResolvConf(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		defer netutils.SetupTestNetNS(t)()
	}

	tmpResolvConf1 := []byte("search pommesfrites.fr\nnameserver 12.34.56.78\nnameserver 2001:4860:4860::8888\n")
	expectedResolvConf1 := []byte("search pommesfrites.fr\nnameserver 12.34.56.78\n")
	tmpResolvConf2 := []byte("search pommesfrites.fr\nnameserver 112.34.56.78\nnameserver 2001:4860:4860::8888\n")
	expectedResolvConf2 := []byte("search pommesfrites.fr\nnameserver 112.34.56.78\n")
	tmpResolvConf3 := []byte("search pommesfrites.fr\nnameserver 113.34.56.78\n")

	//take a copy of resolv.conf for restoring after test completes
	resolvConfSystem, err := ioutil.ReadFile("/etc/resolv.conf")
	if err != nil {
		t.Fatal(err)
	}
	//cleanup
	defer func() {
		if err := ioutil.WriteFile("/etc/resolv.conf", resolvConfSystem, 0644); err != nil {
			t.Fatal(err)
		}
	}()

	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "testnetwork",
			"AllowNonDefaultBridge": true,
		},
	}
	n, err := createTestNetwork("bridge", "testnetwork", netOption)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	ep, err := n.CreateEndpoint("ep")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ep.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	if err := ioutil.WriteFile("/etc/resolv.conf", tmpResolvConf1, 0644); err != nil {
		t.Fatal(err)
	}

	resolvConfPath := "/tmp/libnetwork_test/resolv.conf"
	defer os.Remove(resolvConfPath)

	sb1, err := controller.NewSandbox(containerID, libnetwork.OptionResolvConfPath(resolvConfPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sb1.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep.Join(sb1)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}

	finfo, err := os.Stat(resolvConfPath)
	if err != nil {
		t.Fatal(err)
	}

	fmode := (os.FileMode)(0644)
	if finfo.Mode() != fmode {
		t.Fatalf("Expected file mode %s, got %s", fmode.String(), finfo.Mode().String())
	}

	content, err := ioutil.ReadFile(resolvConfPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(content, expectedResolvConf1) {
		fmt.Printf("\n%v\n%v\n", expectedResolvConf1, content)
		t.Fatalf("Expected:\n%s\nGot:\n%s", string(expectedResolvConf1), string(content))
	}

	err = ep.Leave(sb1)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}

	if err := ioutil.WriteFile("/etc/resolv.conf", tmpResolvConf2, 0644); err != nil {
		t.Fatal(err)
	}

	sb2, err := controller.NewSandbox(containerID+"_2", libnetwork.OptionResolvConfPath(resolvConfPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sb2.Delete(); err != nil {
			t.Fatal(err)
		}
	}()

	err = ep.Join(sb2)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}

	content, err = ioutil.ReadFile(resolvConfPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(content, expectedResolvConf2) {
		t.Fatalf("Expected:\n%s\nGot:\n%s", string(expectedResolvConf2), string(content))
	}

	if err := ioutil.WriteFile(resolvConfPath, tmpResolvConf3, 0644); err != nil {
		t.Fatal(err)
	}

	err = ep.Leave(sb2)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}

	err = ep.Join(sb2)
	runtime.LockOSThread()
	if err != nil {
		t.Fatal(err)
	}

	content, err = ioutil.ReadFile(resolvConfPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(content, tmpResolvConf3) {
		t.Fatalf("Expected:\n%s\nGot:\n%s", string(tmpResolvConf3), string(content))
	}
}

func TestInvalidRemoteDriver(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		t.Skip("Skipping test when not running inside a Container")
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	if server == nil {
		t.Fatal("Failed to start a HTTP Server")
	}
	defer server.Close()

	type pluginRequest struct {
		name string
	}

	mux.HandleFunc("/Plugin.Activate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.plugins.v1+json")
		fmt.Fprintln(w, `{"Implements": ["InvalidDriver"]}`)
	})

	if err := os.MkdirAll("/usr/share/docker/plugins", 0755); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll("/usr/share/docker/plugins"); err != nil {
			t.Fatal(err)
		}
	}()

	if err := ioutil.WriteFile("/usr/share/docker/plugins/invalid-network-driver.spec", []byte(server.URL), 0644); err != nil {
		t.Fatal(err)
	}

	controller, err := libnetwork.New()
	if err != nil {
		t.Fatal(err)
	}

	_, err = controller.NewNetwork("invalid-network-driver", "dummy",
		libnetwork.NetworkOptionGeneric(getEmptyGenericOption()))
	if err == nil {
		t.Fatal("Expected to fail. But instead succeeded")
	}

	if err != plugins.ErrNotImplements {
		t.Fatalf("Did not fail with expected error. Actual error: %v", err)
	}
}

func TestValidRemoteDriver(t *testing.T) {
	if !netutils.IsRunningInContainer() {
		t.Skip("Skipping test when not running inside a Container")
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	if server == nil {
		t.Fatal("Failed to start a HTTP Server")
	}
	defer server.Close()

	type pluginRequest struct {
		name string
	}

	mux.HandleFunc("/Plugin.Activate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.plugins.v1+json")
		fmt.Fprintf(w, `{"Implements": ["%s"]}`, driverapi.NetworkPluginEndpointType)
	})
	mux.HandleFunc(fmt.Sprintf("/%s.CreateNetwork", driverapi.NetworkPluginEndpointType), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.plugins.v1+json")
		fmt.Fprintf(w, "null")
	})

	if err := os.MkdirAll("/usr/share/docker/plugins", 0755); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll("/usr/share/docker/plugins"); err != nil {
			t.Fatal(err)
		}
	}()

	if err := ioutil.WriteFile("/usr/share/docker/plugins/valid-network-driver.spec", []byte(server.URL), 0644); err != nil {
		t.Fatal(err)
	}

	n, err := controller.NewNetwork("valid-network-driver", "dummy",
		libnetwork.NetworkOptionGeneric(getEmptyGenericOption()))
	if err != nil {
		// Only fail if we could not find the plugin driver
		if _, ok := err.(types.NotFoundError); ok {
			t.Fatal(err)
		}
		return
	}
	defer func() {
		if err := n.Delete(); err != nil {
			t.Fatal(err)
		}
	}()
}

var (
	once   sync.Once
	start  = make(chan struct{})
	done   = make(chan chan struct{}, numThreads-1)
	origns = netns.None()
	testns = netns.None()
	sboxes = make([]libnetwork.Sandbox, numThreads)
)

const (
	iterCnt    = 25
	numThreads = 3
	first      = 1
	last       = numThreads
	debug      = false
)

func createGlobalInstance(t *testing.T) {
	var err error
	defer close(start)

	origns, err = netns.Get()
	if err != nil {
		t.Fatal(err)
	}

	if netutils.IsRunningInContainer() {
		testns = origns
	} else {
		testns, err = netns.New()
		if err != nil {
			t.Fatal(err)
		}
	}

	netOption := options.Generic{
		netlabel.GenericData: options.Generic{
			"BridgeName":            "network",
			"AllowNonDefaultBridge": true,
		},
	}

	net1, err := controller.NetworkByName("testhost")
	if err != nil {
		t.Fatal(err)
	}

	net2, err := createTestNetwork("bridge", "network2", netOption)
	if err != nil {
		t.Fatal(err)
	}

	_, err = net1.CreateEndpoint("pep1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = net2.CreateEndpoint("pep2")
	if err != nil {
		t.Fatal(err)
	}

	_, err = net2.CreateEndpoint("pep3")
	if err != nil {
		t.Fatal(err)
	}

	if sboxes[first-1], err = controller.NewSandbox(fmt.Sprintf("%drace", first), libnetwork.OptionUseDefaultSandbox()); err != nil {
		t.Fatal(err)
	}
	for thd := first + 1; thd <= last; thd++ {
		if sboxes[thd-1], err = controller.NewSandbox(fmt.Sprintf("%drace", thd)); err != nil {
			t.Fatal(err)
		}
	}
}

func debugf(format string, a ...interface{}) (int, error) {
	if debug {
		return fmt.Printf(format, a...)
	}

	return 0, nil
}

func parallelJoin(t *testing.T, rc libnetwork.Sandbox, ep libnetwork.Endpoint, thrNumber int) {
	debugf("J%d.", thrNumber)
	var err error

	sb := sboxes[thrNumber-1]
	err = ep.Join(sb)

	runtime.LockOSThread()
	if err != nil {
		if _, ok := err.(types.ForbiddenError); !ok {
			t.Fatalf("thread %d: %v", thrNumber, err)
		}
		debugf("JE%d(%v).", thrNumber, err)
	}
	debugf("JD%d.", thrNumber)
}

func parallelLeave(t *testing.T, rc libnetwork.Sandbox, ep libnetwork.Endpoint, thrNumber int) {
	debugf("L%d.", thrNumber)
	var err error

	cid := fmt.Sprintf("%drace", thrNumber)
	sb := sboxes[thrNumber-1]

	if thrNumber == first {
		err = ep.Leave(sb)
	} else {
		err = sb.Delete()
		// re add sandbox
		defer func() {
			if err == nil {
				var e error
				if sboxes[thrNumber-1], e = controller.NewSandbox(cid); e != nil {
					t.Fatalf("Failed to recreate sandbox %s: %v", cid, e)
				}
			}
		}()
	}

	runtime.LockOSThread()
	if err != nil {
		if _, ok := err.(types.ForbiddenError); !ok {
			t.Fatalf("thread %d: %v", thrNumber, err)
		}
		debugf("LE%d(%v).", thrNumber, err)
	}
	debugf("LD%d.", thrNumber)
}

func runParallelTests(t *testing.T, thrNumber int) {
	var (
		ep  libnetwork.Endpoint
		sb  libnetwork.Sandbox
		err error
	)

	t.Parallel()

	pTest := flag.Lookup("test.parallel")
	if pTest == nil {
		t.Skip("Skipped because test.parallel flag not set;")
	}
	numParallel, err := strconv.Atoi(pTest.Value.String())
	if err != nil {
		t.Fatal(err)
	}
	if numParallel < numThreads {
		t.Skip("Skipped because t.parallel was less than ", numThreads)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if thrNumber == first {
		createGlobalInstance(t)
	}

	if thrNumber != first {
		select {
		case <-start:
		}

		thrdone := make(chan struct{})
		done <- thrdone
		defer close(thrdone)

		if thrNumber == last {
			defer close(done)
		}

		err = netns.Set(testns)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer netns.Set(origns)

	net1, err := controller.NetworkByName("testhost")
	if err != nil {
		t.Fatal(err)
	}
	if net1 == nil {
		t.Fatal("Could not find testhost")
	}

	net2, err := controller.NetworkByName("network2")
	if err != nil {
		t.Fatal(err)
	}
	if net2 == nil {
		t.Fatal("Could not find network2")
	}

	epName := fmt.Sprintf("pep%d", thrNumber)

	if thrNumber == first {
		ep, err = net1.EndpointByName(epName)
	} else {
		ep, err = net2.EndpointByName(epName)
	}

	if err != nil {
		t.Fatal(err)
	}
	if ep == nil {
		t.Fatal("Got nil ep with no error")
	}

	cid := fmt.Sprintf("%drace", thrNumber)
	controller.WalkSandboxes(libnetwork.SandboxContainerWalker(&sb, cid))
	if sb == nil {
		t.Fatalf("Got nil sandbox for container: %s", cid)
	}

	for i := 0; i < iterCnt; i++ {
		parallelJoin(t, sb, ep, thrNumber)
		parallelLeave(t, sb, ep, thrNumber)
	}

	debugf("\n")

	err = ep.Delete()
	if err != nil {
		t.Fatal(err)
	}

	if thrNumber == first {
		for thrdone := range done {
			select {
			case <-thrdone:
			}
		}

		testns.Close()
		err = sb.Delete()
		if err != nil {
			t.Fatal(err)
		}

		ep.Delete()
		if err != nil {
			t.Fatal(err)
		}

		if err := net2.Delete(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParallel1(t *testing.T) {
	runParallelTests(t, 1)
}

func TestParallel2(t *testing.T) {
	runParallelTests(t, 2)
}

func TestParallel3(t *testing.T) {
	runParallelTests(t, 3)
}
