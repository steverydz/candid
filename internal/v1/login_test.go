// Copyright 2015 Canonical Ltd.

package v1_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/juju/idmclient/params"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon.v2-unstable"

	"github.com/CanonicalLtd/blues-identity/idp"
	"github.com/CanonicalLtd/blues-identity/idp/test"
)

type loginSuite struct {
	apiSuite
	netSrv *httptest.Server
}

var _ = gc.Suite(&loginSuite{})

func (s *loginSuite) SetUpSuite(c *gc.C) {
	s.apiSuite.idps = []idp.IdentityProvider{
		test.IdentityProvider,
	}
	s.apiSuite.SetUpSuite(c)
}

func (s *loginSuite) SetUpTest(c *gc.C) {
	s.apiSuite.SetUpTest(c)
	s.netSrv = httptest.NewServer(s.srv)
}

func (s *loginSuite) TearDownTest(c *gc.C) {
	s.netSrv.Close()
	s.apiSuite.TearDownTest(c)
}

func (s *loginSuite) TearDownSuite(c *gc.C) {
	s.apiSuite.TearDownSuite(c)
}

func (s *loginSuite) TestInteractiveLogin(c *gc.C) {
	jar := &testCookieJar{}
	client := httpbakery.NewClient()
	visitor := test.Visitor{
		User: &params.User{
			Username:   "test",
			ExternalID: "http://example.com/+id/test",
			FullName:   "Test User",
			Email:      "test@example.com",
			IDPGroups:  []string{"test1", "test2"},
		},
	}
	u, err := url.Parse(location + "/v1/idp/test/login")
	c.Assert(err, gc.IsNil)
	err = visitor.VisitWebPage(client, map[string]*url.URL{httpbakery.UserInteractionMethod: u})
	c.Assert(err, gc.IsNil)
	c.Assert(jar.cookies, gc.HasLen, 0)
	st := s.pool.GetNoLimit()
	defer st.Close()
	id, err := st.GetIdentity("test")
	c.Assert(err, gc.IsNil)
	c.Assert(id.LastLogin.After(time.Now().Add(-1*time.Second)), gc.Equals, true)
}

func (s *loginSuite) TestNonInteractiveLogin(c *gc.C) {
	jar := &testCookieJar{}
	client := httpbakery.NewClient()
	visitor := test.Visitor{
		User: &params.User{
			Username:   "test",
			ExternalID: "http://example.com/+id/test",
			FullName:   "Test User",
			Email:      "test@example.com",
			IDPGroups:  []string{"test1", "test2"},
		},
	}
	u, err := url.Parse(location + "/v1/idp/test/login")
	c.Assert(err, gc.IsNil)
	err = visitor.VisitWebPage(client, map[string]*url.URL{"test": u})
	c.Assert(err, gc.IsNil)
	c.Assert(jar.cookies, gc.HasLen, 0)
	st := s.pool.GetNoLimit()
	defer st.Close()
	id, err := st.GetIdentity("test")
	c.Assert(err, gc.IsNil)
	c.Assert(id.LastLogin.After(time.Now().Add(-1*time.Second)), gc.Equals, true)
}

func (s *loginSuite) TestLoginFailure(c *gc.C) {
	jar := &testCookieJar{}
	client := httpbakery.NewClient()
	visitor := test.Visitor{
		User: &params.User{},
	}
	u, err := url.Parse(location + "/v1/idp/test/login")
	c.Assert(err, gc.IsNil)
	err = visitor.VisitWebPage(client, map[string]*url.URL{httpbakery.UserInteractionMethod: u})
	c.Assert(err, gc.ErrorMatches, `user "" not found: not found`)
	c.Assert(jar.cookies, gc.HasLen, 0)
}

func (s *loginSuite) assertMacaroon(c *gc.C, jar *testCookieJar, userId string) {
	var ms macaroon.Slice
	for _, cookie := range jar.cookies {
		if strings.HasPrefix(cookie.Name, "macaroon-") {
			data, err := base64.StdEncoding.DecodeString(cookie.Value)
			c.Assert(err, gc.IsNil)
			err = json.Unmarshal(data, &ms)
			c.Assert(err, gc.IsNil)
			break
		}
	}
	c.Assert(ms, gc.Not(gc.HasLen), 0)
	cavs := ms[0].Caveats()
	var found bool
	for _, cav := range cavs {
		if strings.HasPrefix(string(cav.Id), "declared username") {
			found = true
			un := strings.TrimPrefix(string(cav.Id), "declared username ")
			c.Assert(un, gc.Equals, userId)
		}
	}
	c.Assert(found, gc.Equals, true, gc.Commentf("no username  caveat"))
}
