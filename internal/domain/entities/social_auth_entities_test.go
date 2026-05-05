package entities

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSocialLoginRequestUnmarshalAcceptsCanonicalFields(t *testing.T) {
	var req SocialLoginRequest

	err := json.Unmarshal([]byte(`{
		"provider": "apple",
		"idToken": "canonical-id-token",
		"accessToken": "canonical-access-token",
		"code": "canonical-code",
		"redirectUri": "rail://auth",
		"givenName": "Ada",
		"familyName": "Lovelace"
	}`), &req)

	require.NoError(t, err)
	require.Equal(t, SocialProviderApple, req.Provider)
	require.Equal(t, "canonical-id-token", req.IDToken)
	require.Equal(t, "canonical-access-token", req.AccessToken)
	require.Equal(t, "canonical-code", req.Code)
	require.Equal(t, "rail://auth", req.RedirectURI)
	require.Equal(t, "Ada", req.GivenName)
	require.Equal(t, "Lovelace", req.FamilyName)
}

func TestSocialLoginRequestUnmarshalAcceptsAppleAndSnakeCaseAliases(t *testing.T) {
	var req SocialLoginRequest

	err := json.Unmarshal([]byte(`{
		"provider": "apple",
		"id_token": "snake-id-token",
		"access_token": "snake-access-token",
		"authorizationCode": "apple-auth-code",
		"redirect_uri": "rail://auth",
		"given_name": "Grace",
		"family_name": "Hopper"
	}`), &req)

	require.NoError(t, err)
	require.Equal(t, SocialProviderApple, req.Provider)
	require.Equal(t, "snake-id-token", req.IDToken)
	require.Equal(t, "snake-access-token", req.AccessToken)
	require.Equal(t, "apple-auth-code", req.Code)
	require.Equal(t, "rail://auth", req.RedirectURI)
	require.Equal(t, "Grace", req.GivenName)
	require.Equal(t, "Hopper", req.FamilyName)
}

func TestSocialLoginRequestUnmarshalAcceptsExpoIdentityTokenAlias(t *testing.T) {
	var req SocialLoginRequest

	err := json.Unmarshal([]byte(`{
		"provider": "apple",
		"identityToken": "expo-identity-token",
		"authorization_code": "snake-auth-code"
	}`), &req)

	require.NoError(t, err)
	require.Equal(t, "expo-identity-token", req.IDToken)
	require.Equal(t, "snake-auth-code", req.Code)
}

func TestSocialLoginRequestUnmarshalAcceptsSnakeCaseIdentityTokenAlias(t *testing.T) {
	var req SocialLoginRequest

	err := json.Unmarshal([]byte(`{
		"provider": "apple",
		"identity_token": "snake-identity-token"
	}`), &req)

	require.NoError(t, err)
	require.Equal(t, "snake-identity-token", req.IDToken)
}
