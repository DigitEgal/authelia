package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/authelia/authelia/v4/internal/duo"
	"github.com/authelia/authelia/v4/internal/mocks"
	"github.com/authelia/authelia/v4/internal/models"
	"github.com/authelia/authelia/v4/internal/regulation"
)

type SecondFactorDuoPostSuite struct {
	suite.Suite
	mock *mocks.MockAutheliaCtx
}

func (s *SecondFactorDuoPostSuite) SetupTest() {
	s.mock = mocks.NewMockAutheliaCtx(s.T())
	userSession := s.mock.Ctx.GetSession()
	userSession.Username = testUsername
	err := s.mock.Ctx.SaveSession(userSession)
	require.NoError(s.T(), err)
}

func (s *SecondFactorDuoPostSuite) TearDownTest() {
	s.mock.Close()
}

func (s *SecondFactorDuoPostSuite) TestShouldEnroll() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(nil, errors.New("no Duo device and method saved"))

	var enrollURL = "https://api-example.duosecurity.com/portal?code=1234567890ABCDEF&akey=12345ABCDEFGHIJ67890"

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = enroll
	preAuthResponse.EnrollPortalURL = enrollURL

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	s.mock.Assert200OK(s.T(), DuoSignResponse{
		Result:    enroll,
		EnrollURL: enrollURL,
	})
}

func (s *SecondFactorDuoPostSuite) TestShouldAutoSelect() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().LoadPreferredDuoDevice(s.mock.Ctx, "john").Return(nil, errors.New("no Duo device and method saved"))

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
		{Capabilities: []string{"auto", "sms", "mobile_otp"}, Number: "+123456789****", Device: "1234567890ABCDEFGHIJ", DisplayName: "Test Device 2"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	s.mock.StorageMock.EXPECT().
		SavePreferredDuoDevice(s.mock.Ctx, models.DuoDevice{Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}).
		Return(nil)

	s.mock.StorageMock.
		EXPECT().
		AppendAuthenticationLog(s.mock.Ctx, gomock.Eq(models.AuthenticationAttempt{
			Username:   "john",
			Successful: true,
			Banned:     false,
			Time:       s.mock.Clock.Now(),
			Type:       regulation.AuthTypeDuo,
			RemoteIP:   models.NewNullIPFromString("0.0.0.0"),
		})).
		Return(nil)

	values = url.Values{}
	values.Set("username", "john")
	values.Set("ipaddr", s.mock.Ctx.RemoteIP().String())
	values.Set("factor", "push")
	values.Set("device", "12345ABCDEFGHIJ67890")
	values.Set("pushinfo", "target%20url=https://target.example.com")

	authResponse := duo.AuthResponse{}
	authResponse.Result = allow

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&authResponse, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{TargetURL: "https://target.example.com"})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)
	assert.Equal(s.T(), 200, s.mock.Ctx.Response.StatusCode())
}

func (s *SecondFactorDuoPostSuite) TestShouldDenyAutoSelect() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(nil, errors.New("no Duo device and method saved"))

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = deny

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	values = url.Values{}
	values.Set("username", "john")
	values.Set("ipaddr", s.mock.Ctx.RemoteIP().String())
	values.Set("factor", "push")
	values.Set("device", "12345ABCDEFGHIJ67890")

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	s.mock.Assert200OK(s.T(), DuoSignResponse{
		Result: deny,
	})
}

func (s *SecondFactorDuoPostSuite) TestShouldFailAutoSelect() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(nil, errors.New("no Duo device and method saved"))

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Any()).Return(nil, fmt.Errorf("Connnection error"))

	bodyBytes, err := json.Marshal(signDuoRequestBody{TargetURL: "https://target.example.com"})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	s.mock.Assert401KO(s.T(), "Authentication failed, please retry later.")
}

func (s *SecondFactorDuoPostSuite) TestShouldDeleteOldDeviceAndEnroll() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "NOTEXISTENT", Method: "push"}, nil)

	var enrollURL = "https://api-example.duosecurity.com/portal?code=1234567890ABCDEF&akey=12345ABCDEFGHIJ67890"

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = enroll
	preAuthResponse.EnrollPortalURL = enrollURL

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	s.mock.StorageMock.EXPECT().DeletePreferredDuoDevice(s.mock.Ctx, "john").Return(nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	s.mock.Assert200OK(s.T(), DuoSignResponse{
		Result:    enroll,
		EnrollURL: enrollURL,
	})
}

func (s *SecondFactorDuoPostSuite) TestShouldDeleteOldDeviceAndCallPreauthAPIWithInvalidDevicesAndEnroll() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "NOTEXISTENT", Method: "push"}, nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"sms"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	s.mock.StorageMock.EXPECT().DeletePreferredDuoDevice(s.mock.Ctx, "john").Return(nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	s.mock.Assert200OK(s.T(), DuoSignResponse{
		Result: enroll,
	})
}

func (s *SecondFactorDuoPostSuite) TestShouldUseOldDeviceAndSelect() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "NOTEXISTENT", Method: "push"}, nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: "+123456789****", Device: "1234567890ABCDEFGHIJ", DisplayName: "Test Device 2"},
		{Capabilities: []string{"auto", "sms", "mobile_otp"}, Number: "+123456789****", Device: "1234567890ABCDEFGHIJ", DisplayName: "Test Device 3"},
	}

	var apiDevices = []DuoDevice{
		{Capabilities: []string{"push"}, Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
		{Capabilities: []string{"push"}, Device: "1234567890ABCDEFGHIJ", DisplayName: "Test Device 2"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)
	s.mock.Assert200OK(s.T(), DuoDevicesResponse{Result: auth, Devices: apiDevices})
}

func (s *SecondFactorDuoPostSuite) TestShouldUseInvalidMethodAndAutoSelect() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "invalidmethod"}, nil)

	s.mock.StorageMock.
		EXPECT().
		AppendAuthenticationLog(s.mock.Ctx, gomock.Eq(models.AuthenticationAttempt{
			Username:   "john",
			Successful: true,
			Banned:     false,
			Time:       s.mock.Clock.Now(),
			Type:       regulation.AuthTypeDuo,
			RemoteIP:   models.NewNullIPFromString("0.0.0.0"),
		})).
		Return(nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	s.mock.StorageMock.EXPECT().
		SavePreferredDuoDevice(s.mock.Ctx, models.DuoDevice{Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}).
		Return(nil)

	values = url.Values{}
	values.Set("username", "john")
	values.Set("ipaddr", s.mock.Ctx.RemoteIP().String())
	values.Set("factor", "push")
	values.Set("device", "12345ABCDEFGHIJ67890")
	values.Set("pushinfo", "target%20url=https://target.example.com")

	authResponse := duo.AuthResponse{}
	authResponse.Result = allow

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&authResponse, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{TargetURL: "https://target.example.com"})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)
	assert.Equal(s.T(), 200, s.mock.Ctx.Response.StatusCode())
}

func (s *SecondFactorDuoPostSuite) TestShouldCallDuoPreauthAPIAndAllowAccess() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = allow

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{TargetURL: "https://target.example.com"})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	assert.Equal(s.T(), 200, s.mock.Ctx.Response.StatusCode())
}

func (s *SecondFactorDuoPostSuite) TestShouldCallDuoPreauthAPIAndDenyAccess() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = deny

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	values = url.Values{}
	values.Set("username", "john")
	values.Set("ipaddr", s.mock.Ctx.RemoteIP().String())
	values.Set("factor", "push")
	values.Set("device", "12345ABCDEFGHIJ67890")

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	assert.Equal(s.T(), 401, s.mock.Ctx.Response.StatusCode())
}

func (s *SecondFactorDuoPostSuite) TestShouldCallDuoPreauthAPIAndFail() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Any()).Return(nil, fmt.Errorf("Connnection error"))

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	s.mock.Assert401KO(s.T(), "Authentication failed, please retry later.")
}

func (s *SecondFactorDuoPostSuite) TestShouldCallDuoAPIAndDenyAccess() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	s.mock.StorageMock.
		EXPECT().
		AppendAuthenticationLog(s.mock.Ctx, gomock.Eq(models.AuthenticationAttempt{
			Username:   "john",
			Successful: false,
			Banned:     false,
			Time:       s.mock.Clock.Now(),
			Type:       regulation.AuthTypeDuo,
			RemoteIP:   models.NewNullIPFromString("0.0.0.0"),
		})).
		Return(nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	values = url.Values{}
	values.Set("username", "john")
	values.Set("ipaddr", s.mock.Ctx.RemoteIP().String())
	values.Set("factor", "push")
	values.Set("device", "12345ABCDEFGHIJ67890")

	response := duo.AuthResponse{}
	response.Result = deny

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&response, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	assert.Equal(s.T(), 401, s.mock.Ctx.Response.StatusCode())
}

func (s *SecondFactorDuoPostSuite) TestShouldCallDuoAPIAndFail() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Any()).Return(nil, fmt.Errorf("Connnection error"))

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)

	s.mock.Assert401KO(s.T(), "Authentication failed, please retry later.")
}

func (s *SecondFactorDuoPostSuite) TestShouldRedirectUserToDefaultURL() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	s.mock.StorageMock.
		EXPECT().
		AppendAuthenticationLog(s.mock.Ctx, gomock.Eq(models.AuthenticationAttempt{
			Username:   "john",
			Successful: true,
			Banned:     false,
			Time:       s.mock.Clock.Now(),
			Type:       regulation.AuthTypeDuo,
			RemoteIP:   models.NewNullIPFromString("0.0.0.0"),
		})).
		Return(nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	response := duo.AuthResponse{}
	response.Result = allow

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Any()).Return(&response, nil)

	s.mock.Ctx.Configuration.DefaultRedirectionURL = testRedirectionURL

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)
	s.mock.Assert200OK(s.T(), redirectResponse{
		Redirect: testRedirectionURL,
	})
}

func (s *SecondFactorDuoPostSuite) TestShouldNotReturnRedirectURL() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	s.mock.StorageMock.
		EXPECT().
		AppendAuthenticationLog(s.mock.Ctx, gomock.Eq(models.AuthenticationAttempt{
			Username:   "john",
			Successful: true,
			Banned:     false,
			Time:       s.mock.Clock.Now(),
			Type:       regulation.AuthTypeDuo,
			RemoteIP:   models.NewNullIPFromString("0.0.0.0"),
		})).
		Return(nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	response := duo.AuthResponse{}
	response.Result = allow

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Any()).Return(&response, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)
	s.mock.Assert200OK(s.T(), nil)
}

func (s *SecondFactorDuoPostSuite) TestShouldRedirectUserToSafeTargetURL() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	s.mock.StorageMock.
		EXPECT().
		AppendAuthenticationLog(s.mock.Ctx, gomock.Eq(models.AuthenticationAttempt{
			Username:   "john",
			Successful: true,
			Banned:     false,
			Time:       s.mock.Clock.Now(),
			Type:       regulation.AuthTypeDuo,
			RemoteIP:   models.NewNullIPFromString("0.0.0.0"),
		})).
		Return(nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	response := duo.AuthResponse{}
	response.Result = allow

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Any()).Return(&response, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{
		TargetURL: "https://mydomain.local",
	})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)
	s.mock.Assert200OK(s.T(), redirectResponse{
		Redirect: "https://mydomain.local",
	})
}

func (s *SecondFactorDuoPostSuite) TestShouldNotRedirectToUnsafeURL() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	s.mock.StorageMock.
		EXPECT().
		AppendAuthenticationLog(s.mock.Ctx, gomock.Eq(models.AuthenticationAttempt{
			Username:   "john",
			Successful: true,
			Banned:     false,
			Time:       s.mock.Clock.Now(),
			Type:       regulation.AuthTypeDuo,
			RemoteIP:   models.NewNullIPFromString("0.0.0.0"),
		})).
		Return(nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	response := duo.AuthResponse{}
	response.Result = allow

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Any()).Return(&response, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{
		TargetURL: "http://mydomain.local",
	})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)
	s.mock.Assert200OK(s.T(), nil)
}

func (s *SecondFactorDuoPostSuite) TestShouldRegenerateSessionForPreventingSessionFixation() {
	duoMock := mocks.NewMockAPI(s.mock.Ctrl)

	s.mock.StorageMock.EXPECT().
		LoadPreferredDuoDevice(s.mock.Ctx, "john").
		Return(&models.DuoDevice{ID: 1, Username: "john", Device: "12345ABCDEFGHIJ67890", Method: "push"}, nil)

	s.mock.StorageMock.
		EXPECT().
		AppendAuthenticationLog(s.mock.Ctx, gomock.Eq(models.AuthenticationAttempt{
			Username:   "john",
			Successful: true,
			Banned:     false,
			Time:       s.mock.Clock.Now(),
			Type:       regulation.AuthTypeDuo,
			RemoteIP:   models.NewNullIPFromString("0.0.0.0"),
		})).
		Return(nil)

	var duoDevices = []duo.Device{
		{Capabilities: []string{"auto", "push", "sms", "mobile_otp"}, Number: " ", Device: "12345ABCDEFGHIJ67890", DisplayName: "Test Device 1"},
	}

	values := url.Values{}
	values.Set("username", "john")

	preAuthResponse := duo.PreAuthResponse{}
	preAuthResponse.Result = auth
	preAuthResponse.Devices = duoDevices

	duoMock.EXPECT().PreAuthCall(s.mock.Ctx, gomock.Eq(values)).Return(&preAuthResponse, nil)

	response := duo.AuthResponse{}
	response.Result = allow

	duoMock.EXPECT().AuthCall(s.mock.Ctx, gomock.Any()).Return(&response, nil)

	bodyBytes, err := json.Marshal(signDuoRequestBody{
		TargetURL: "http://mydomain.local",
	})
	s.Require().NoError(err)
	s.mock.Ctx.Request.SetBody(bodyBytes)

	r := regexp.MustCompile("^authelia_session=(.*); path=")
	res := r.FindAllStringSubmatch(string(s.mock.Ctx.Response.Header.PeekCookie("authelia_session")), -1)

	SecondFactorDuoPost(duoMock)(s.mock.Ctx)
	s.mock.Assert200OK(s.T(), nil)

	s.Assert().NotEqual(
		res[0][1],
		string(s.mock.Ctx.Request.Header.Cookie("authelia_session")))
}

func TestRunSecondFactorDuoPostSuite(t *testing.T) {
	s := new(SecondFactorDuoPostSuite)
	suite.Run(t, s)
}
