package infrared

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/haveachin/infrared/protocol"
	"github.com/haveachin/infrared/protocol/login"
	"github.com/haveachin/infrared/protocol/status"
)

type Server struct {
	ID                string
	Domains           []string
	Dialer            net.Dialer
	Address           string
	SendProxyProtocol bool
	SendRealIP        bool
	DisconnectMessage string
	OnlineStatus      OnlineStatusResponse
	OfflineStatus     OfflineStatusResponse
	WebhookIDs        []string
	Log               logr.Logger
}

func (s Server) Dial() (Conn, error) {
	c, err := s.Dialer.Dial("tcp", s.Address)
	if err != nil {
		return nil, err
	}

	return newConn(c), nil
}

func (s Server) replaceTemplates(c ProcessingConn, msg string) string {
	tmpls := map[string]string{
		"username":      c.username,
		"now":           time.Now().Format(time.RFC822),
		"remoteAddress": c.RemoteAddr().String(),
		"localAddress":  c.LocalAddr().String(),
		"domain":        c.srvHost,
		"serverAddress": s.Address,
	}

	for k, v := range tmpls {
		msg = strings.Replace(msg, fmt.Sprintf("{{%s}}", k), v, -1)
	}

	return msg
}

func (s Server) handleOfflineStatusRequest(c ProcessingConn) error {
	respJSON, err := s.OfflineStatus.ResponseJSON()
	if err != nil {
		return err
	}

	bb, err := json.Marshal(respJSON)
	if err != nil {
		return err
	}

	respPk := status.ClientBoundResponse{
		JSONResponse: protocol.String(bb),
	}.Marshal()

	if err := c.WritePacket(respPk); err != nil {
		return err
	}

	pingPk, err := c.ReadPacket()
	if err != nil {
		return err
	}

	return c.WritePacket(pingPk)
}

func (s Server) handleOfflineLoginRequest(c ProcessingConn) error {
	msg := s.replaceTemplates(c, s.DisconnectMessage)

	pk := login.ClientBoundDisconnect{
		Reason: protocol.Chat(fmt.Sprintf("{\"text\":\"%s\"}", msg)),
	}.Marshal()

	return c.WritePacket(pk)
}

func (s Server) handleOffline(c ProcessingConn) error {
	if c.handshake.IsStatusRequest() {
		return s.handleOfflineStatusRequest(c)
	}

	return s.handleOfflineLoginRequest(c)
}

func (s Server) overrideStatusResponse(c ProcessingConn, rc Conn) error {
	pk, err := rc.ReadPacket()
	if err != nil {
		return err
	}

	respPk, err := status.UnmarshalClientBoundResponse(pk)
	if err != nil {
		return err
	}

	var respJSON status.ResponseJSON
	if err := json.Unmarshal([]byte(respPk.JSONResponse), &respJSON); err != nil {
		return err
	}

	respJSON, err = s.OnlineStatus.ResponseJSON(respJSON)
	if err != nil {
		return err
	}

	bb, err := json.Marshal(respJSON)
	if err != nil {
		return err
	}

	respPk.JSONResponse = protocol.String(bb)

	if err := c.WritePacket(respPk.Marshal()); err != nil {
		return err
	}

	return nil
}

func (s Server) ProcessConnection(c ProcessingConn) (ProcessedConn, error) {
	rc, err := s.Dial()
	if err != nil {
		if err := s.handleOffline(c); err != nil {
			return ProcessedConn{}, err
		}
		return ProcessedConn{}, err
	}

	if err := rc.WritePackets(c.readPks...); err != nil {
		rc.Close()
		return ProcessedConn{}, err
	}

	if c.handshake.IsStatusRequest() {
		if err := s.overrideStatusResponse(c, rc); err != nil {
			rc.Close()
			return ProcessedConn{}, err
		}
	}

	return ProcessedConn{
		ProcessingConn: c,
		ServerConn:     rc,
		ServerID:       s.ID,
	}, nil
}

type ServerGateway struct {
	Servers []Server
	Log     logr.Logger

	srvs map[string]*Server
}

func (sg *ServerGateway) mapServers() {
	sg.srvs = map[string]*Server{}

	for _, server := range sg.Servers {
		for _, host := range server.Domains {
			hostLower := strings.ToLower(host)
			sg.srvs[hostLower] = &server
		}
	}
}

func (sg ServerGateway) Start(srvChan <-chan ProcessingConn, poolChan chan<- ProcessedConn) {
	sg.mapServers()

	for {
		c, ok := <-srvChan
		if !ok {
			break
		}

		hostLower := strings.ToLower(c.srvHost)
		srv, ok := sg.srvs[hostLower]
		if !ok {
			sg.Log.Info("invlaid server host",
				"serverId", hostLower,
				"remoteAddress", c.RemoteAddr(),
			)
			continue
		}

		sg.Log.Info("connecting client",
			"serverId", hostLower,
			"remoteAddress", c.RemoteAddr(),
		)
		pc, err := srv.ProcessConnection(c)
		if err != nil {
			continue
		}
		poolChan <- pc
	}
}
