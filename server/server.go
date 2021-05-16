package server

import (
	"errors"

	"github.com/haveachin/infrared"
	"github.com/haveachin/infrared/connection"
	"github.com/haveachin/infrared/protocol"
)

type LoginServer interface {
	Login(conn connection.LoginConnection) error
}

type StatusServer interface {
	Status() protocol.Packet
}

// Server will act as abstraction layer between connection and proxy
type Server interface {
	LoginServer
	StatusServer
}

var (
	ErrCantConnectWithServer = errors.New("cant connect with server")
)

type MCServer struct {
	ConnFactory         func() connection.ServerConnection //Probably needs better names, or a different code structure
	serverConn          connection.ServerConnection
	OnlineConfigStatus  protocol.Packet
	OfflineConfigStatus protocol.Packet
	UseConfigStatus     bool
}

//Controller layer
func (s *MCServer) Status() protocol.Packet {
	if s.serverConn == nil {
		s.serverConn = s.ConnFactory()
	}
	pk, err := s.serverConn.Status()
	if err == nil {
		if s.UseConfigStatus {
			pk = s.OnlineConfigStatus
		}
	} else if s.UseConfigStatus {
		pk = s.OfflineConfigStatus
	} else {
		pk, _ = infrared.StatusConfig{}.StatusResponsePacket()
	}
	return pk
}

func (s *MCServer) Login(conn connection.LoginConnection) error {
	sConn := s.serverConn
	if sConn == nil {
		sConn = s.ConnFactory()
	}

	hs, err := conn.HS()
	if err != nil {
		return err
	}

	if err = sConn.SendPK(hs); err != nil {
		return err
	}

	pk, err := conn.LoginStart()
	if err != nil {
		return err
	}

	return sConn.SendPK(pk)
}

// Use case layer
func HandleStatusRequest(conn connection.StatusConnection, server Server) error {
	status := server.Status()
	return conn.SendStatus(status)
}

func HandleLoginRequest(conn connection.LoginConnection, server Server) error {
	return server.Login(conn)
}