package nsmd

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/golang/protobuf/ptypes/empty"

	"github.com/go-errors/errors"

	"github.com/ligato/networkservicemesh/controlplane/pkg/apis/local/connection"
	"github.com/ligato/networkservicemesh/controlplane/pkg/apis/local/networkservice"
	"github.com/ligato/networkservicemesh/controlplane/pkg/apis/registry"
	"github.com/ligato/networkservicemesh/controlplane/pkg/model"
	dataplaneapi "github.com/ligato/networkservicemesh/dataplane/pkg/apis/dataplane"
	"github.com/ligato/networkservicemesh/pkg/tools"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

const (
	// nseConnectionTimeout defines a timoute for NSM to succeed connection to NSE (seconds)
	nseConnectionTimeout = 15 * time.Second
)

type networkServiceServer struct {
	model     model.Model
	workspace *Workspace
}

func NewNetworkServiceServer(model model.Model, workspace *Workspace) networkservice.NetworkServiceServer {
	return &networkServiceServer{
		model:     model,
		workspace: workspace,
	}
}

func (srv *networkServiceServer) Request(ctx context.Context, request *networkservice.NetworkServiceRequest) (*connection.Connection, error) {
	logrus.Infof("Received request from client to connect to NetworkService: %v", request)
	// Make sure its a valid request
	err := request.IsValid()
	if err != nil {
		logrus.Error(err)
		return nil, err
	}
	// Create a ConnectId for the request.GetConnection()
	request.GetConnection().Id = srv.model.ConnectionId()
	// TODO: Mechanism selection
	request.GetConnection().Mechanism = request.MechanismPreferences[0]

	// Get endpoints
	endpoints := srv.model.GetNetworkServiceEndpoints(request.GetConnection().GetNetworkService())
	if len(endpoints) == 0 {
		return nil, errors.New(fmt.Sprintf("netwwork service '%s' not found", request.Connection.NetworkService))
	}

	// Select endpoint at random
	idx := rand.Intn(len(endpoints))
	endpoint := endpoints[idx]
	if endpoint == nil {
		return nil, errors.New("should not see this error, scaffolding called")
	}

	// get dataplane
	dp, err := srv.model.SelectDataplane()
	if err != nil {
		return nil, err
	}

	logrus.Infof("Preparing to program dataplane: %v...", dp)

	dataplaneConn, err := tools.SocketOperationCheck(dp.SocketLocation)
	if err != nil {
		return nil, err
	}
	defer dataplaneConn.Close()
	dataplaneClient := dataplaneapi.NewDataplaneClient(dataplaneConn)

	dpCtx, dpCancel := context.WithTimeout(context.Background(), nseConnectionTimeout)
	defer dpCancel()

	var dpApiConnection *dataplaneapi.CrossConnect
	// If NSE is local, build parameters
	if srv.model.GetNsmUrl() == endpoint.Labels[registry.NsmUrlKey] {
		workspace := WorkSpaceRegistry().WorkspaceByEndpoint(endpoint)
		if workspace == nil {
			err := fmt.Errorf("cannot find workspace for endpoint %v", endpoint)
			logrus.Error(err)
			return nil, err
		}
		nseConn, err := tools.SocketOperationCheck(workspace.NsmClientSocket())
		if err != nil {
			logrus.Errorf("unable to connect to nse %v", endpoint)
			return nil, err
		}
		defer nseConn.Close()

		client := networkservice.NewNetworkServiceClient(nseConn)
		message := &networkservice.NetworkServiceRequest{
			Connection: &connection.Connection{
				// TODO track connection ids
				Id:             srv.model.ConnectionId(),
				NetworkService: endpoint.GetNetworkServiceName(),
				Mechanism: &connection.Mechanism{
					Type:       connection.MechanismType_KERNEL_INTERFACE,
					Parameters: map[string]string{},
				},
				Context: request.GetConnection().GetContext(),
				Labels:  nil,
			},
		}
		nseConnection, e := client.Request(ctx, message)
		request.GetConnection().Context = nseConnection.Context
		err = nseConnection.IsComplete()
		if err != nil {
			err = fmt.Errorf("failure Validating NSE Connection: %s", err)
			return nil, err
		}
		err = request.GetConnection().IsComplete()
		if err != nil {
			err = fmt.Errorf("failure Validating NSE Connection: %s", err)
			return nil, err
		}

		if e != nil {
			logrus.Errorf("error requesting networkservice from %+v with message %#v error: %s", endpoint, message, e)
			return nil, e
		}

		dpApiConnection = &dataplaneapi.CrossConnect{
			Id:      request.GetConnection().GetId(),
			Payload: endpoint.Payload,
			Source: &dataplaneapi.CrossConnect_LocalSource{
				request.GetConnection(),
			},
			Destination: &dataplaneapi.CrossConnect_LocalDestination{
				nseConnection,
			},
		}
	} else {
		// TODO connection is remote, send to nsm
	}
	logrus.Infof("Sending request to dataplane: %v", dpApiConnection)
	rv, err := dataplaneClient.Request(dpCtx, dpApiConnection)
	if err != nil {
		logrus.Errorf("Dataplane request failed: %s", err)
		return nil, err
	}
	// TODO - be more cautious here about bad return values from Dataplane
	return rv.GetSource().(*dataplaneapi.CrossConnect_LocalSource).LocalSource, nil
}

func (srv *networkServiceServer) Close(context.Context, *connection.Connection) (*empty.Empty, error) {
	panic("implement me")
}

func (srv *networkServiceServer) Monitor(*connection.Connection, networkservice.NetworkService_MonitorServer) error {
	panic("implement me")
}

func (srv *networkServiceServer) MonitorConnections(*empty.Empty, networkservice.NetworkService_MonitorConnectionsServer) error {
	panic("implement me")
}