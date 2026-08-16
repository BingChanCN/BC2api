package service

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

type catProxiesTransportFault int

const (
	catProxiesTransportFaultIgnored catProxiesTransportFault = iota
	catProxiesTransportFaultSession
	catProxiesTransportFaultSessionImmediate
	catProxiesTransportFaultCredential
	catProxiesTransportFaultRateLimited
	catProxiesTransportFaultGateway
)

// classifyCatProxiesTransportFault is deliberately conservative. This layer has
// no reliable CONNECT response object, so it recognizes typed TCP/EOF/TLS faults
// and explicit proxy-authentication failures, but does not infer arbitrary HTTP
// tunnel status codes or DNS ownership from error strings.
func classifyCatProxiesTransportFault(err error) catProxiesTransportFault {
	if err == nil || errors.Is(err, context.Canceled) {
		return catProxiesTransportFaultIgnored
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "407") || strings.Contains(message, "proxy authentication required") || strings.Contains(message, "username/password authentication failed") {
		return catProxiesTransportFaultCredential
	}
	if strings.Contains(message, "429") {
		return catProxiesTransportFaultRateLimited
	}
	if strings.Contains(message, "proxyconnect tcp") &&
		(strings.Contains(message, "502") || strings.Contains(message, "503") || strings.Contains(message, "504")) {
		return catProxiesTransportFaultSessionImmediate
	}
	if strings.Contains(message, "no available proxy") || strings.Contains(message, "no proxy available") || strings.Contains(message, "no available exit") {
		return catProxiesTransportFaultSessionImmediate
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return catProxiesTransportFaultGateway
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
		return catProxiesTransportFaultSession
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		var opErr *net.OpError
		if errors.As(err, &opErr) && (opErr.Op == "dial" || opErr.Op == "connect") {
			return catProxiesTransportFaultSession
		}
	}
	var tlsHeader tls.RecordHeaderError
	if errors.As(err, &tlsHeader) {
		return catProxiesTransportFaultSession
	}
	if strings.Contains(message, "tls handshake") || strings.Contains(message, "connection reset by peer") || strings.Contains(message, "unexpected eof") || strings.Contains(message, "proxyconnect tcp") {
		return catProxiesTransportFaultSession
	}
	return catProxiesTransportFaultIgnored
}

func (s *OpenAIGatewayService) SetManagedProxyRuntime(runtime *ManagedProxyRuntimeService) {
	if s != nil {
		s.managedProxyRuntime = runtime
	}
}

func (s *OpenAIGatewayService) notifyManagedProxyTransportFailure(ctx context.Context, account *Account, err error) {
	if s == nil || s.managedProxyRuntime == nil || account == nil || account.Proxy == nil {
		return
	}
	fault := classifyCatProxiesTransportFault(err)
	redactedErr := redactCatProxiesProxyError(err, account.Proxy)
	switch fault {
	case catProxiesTransportFaultCredential:
		s.managedProxyRuntime.NotifyProviderCredentialError(ctx, account.ID, account.Proxy.ID, account.Proxy.UpdatedAt, redactedErr)
	case catProxiesTransportFaultRateLimited:
		s.managedProxyRuntime.NotifyRateLimit(ctx, account.ID)
	case catProxiesTransportFaultSessionImmediate:
		s.managedProxyRuntime.NotifyImmediateTransportFailure(ctx, account.ID, account.Proxy.ID, redactedErr)
	case catProxiesTransportFaultSession:
		s.managedProxyRuntime.NotifyTransportFailure(ctx, account.ID, account.Proxy.ID, redactedErr)
	case catProxiesTransportFaultGateway:
		s.managedProxyRuntime.NotifyGatewayFailure(ctx, account.ID, account.Proxy.ID, redactedErr)
	}
}

func (s *OpenAIGatewayService) notifyManagedProxyTransportSuccess(ctx context.Context, account *Account) {
	if s != nil && s.managedProxyRuntime != nil && account != nil && account.Proxy != nil {
		s.managedProxyRuntime.NotifyTransportSuccess(ctx, account.ID, account.Proxy.ID)
	}
}

type ManagedProxyTransportHook struct{}

func ProvideManagedProxyTransportHook(openAI *OpenAIGatewayService, runtime *ManagedProxyRuntimeService) *ManagedProxyTransportHook {
	openAI.SetManagedProxyRuntime(runtime)
	return &ManagedProxyTransportHook{}
}
