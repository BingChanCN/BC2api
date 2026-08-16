package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyCatProxiesTransportFaultTreats407AsCredentialError(t *testing.T) {
	require.Equal(t, catProxiesTransportFaultCredential, classifyCatProxiesTransportFault(errors.New("HTTP 407")))
	require.Equal(t, catProxiesTransportFaultCredential, classifyCatProxiesTransportFault(errors.New("proxy authentication required")))
}

func TestClassifyCatProxiesTransportFaultUsesBackoffFor429WithoutSessionRotation(t *testing.T) {
	require.Equal(t, catProxiesTransportFaultRateLimited, classifyCatProxiesTransportFault(errors.New("HTTP 429 Too Many Requests")))
}

func TestClassifyCatProxiesTransportFaultRotatesImmediatelyForConnectGatewayFailure(t *testing.T) {
	for _, status := range []string{"502", "503", "504"} {
		err := errors.New("proxyconnect tcp: upstream proxy returned " + status)
		require.Equal(t, catProxiesTransportFaultSessionImmediate, classifyCatProxiesTransportFault(err))
	}
	require.Equal(t, catProxiesTransportFaultSessionImmediate, classifyCatProxiesTransportFault(errors.New("no available proxy in selected location")))
}

func TestClassifyCatProxiesTransportFaultDoesNotTreatOrdinaryUpstream503AsProxyFailure(t *testing.T) {
	require.Equal(t, catProxiesTransportFaultIgnored, classifyCatProxiesTransportFault(errors.New("upstream returned HTTP 503")))
}
