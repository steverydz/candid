// Copyright 2014 Canonical Ltd.

package store_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/juju/idmclient/params"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/prometheus/client_golang/prometheus"
	prom "github.com/prometheus/client_model/go"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2/bson"
	"launchpad.net/lpad"

	"github.com/CanonicalLtd/blues-identity/internal/limitpool"
	"github.com/CanonicalLtd/blues-identity/internal/mongodoc"
	"github.com/CanonicalLtd/blues-identity/internal/store"
)

type storeSuite struct {
	testing.IsolatedMgoSuite
	pool *store.Pool
}

var _ = gc.Suite(&storeSuite{})

func (s *storeSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	var err error
	s.pool, err = store.NewPool(s.Session.Copy().DB("store-tests"), store.StoreParams{
		MaxMgoSessions: 10,
		PrivateAddr:    "localhost",
	})
	c.Assert(err, gc.IsNil)
}

func (s *storeSuite) TearDownTest(c *gc.C) {
	s.pool.Close()
	s.IsolatedMgoSuite.TearDownTest(c)
}

func (s *storeSuite) TestNew(c *gc.C) {
	db := s.Session.DB("store-tests")

	// Add an identity to the identities collection using mgo directly.
	err := db.C("identities").Insert(&mongodoc.Identity{
		Username:   "who",
		ExternalID: "http://example.com/who",
		Email:      "who@example.com",
		FullName:   "Who Am I",
		Groups: []string{
			"group1",
		},
	})
	c.Assert(err, gc.IsNil)

	// Set up a new store.
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Retrieve the identity using the store object.
	var doc mongodoc.Identity
	err = store.DB.Identities().Find(nil).One(&doc)
	c.Assert(err, gc.IsNil)
	c.Assert(doc.Username, gc.Equals, "who")
	c.Assert(doc.ExternalID, gc.Equals, "http://example.com/who")
	c.Assert(doc.Email, gc.Equals, "who@example.com")
	c.Assert(doc.FullName, gc.Equals, "Who Am I")
	c.Assert(doc.Groups, gc.DeepEquals, []string{"group1"})
}

var insertIdentityTests = []struct {
	about     string
	identity  *mongodoc.Identity
	expectErr string
}{{
	about: "insert interactive user",
	identity: &mongodoc.Identity{
		Username:   "test",
		ExternalID: "http://example.com/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
		},
		SSHKeys: []string{
			"ssh-key-1",
		},
		ExtraInfo: map[string][]byte{
			"extra-info-1": []byte("null"),
		},
	},
}, {
	about: "clashing username",
	identity: &mongodoc.Identity{
		Username:   "existing",
		ExternalID: "http://example.com/test-clashing",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
		},
	},
	expectErr: "cannot add user: duplicate username or external_id",
}, {
	about: "clashing external ID",
	identity: &mongodoc.Identity{
		Username:   "test-clashing",
		ExternalID: "http://example.com/existing",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
		},
	},
	expectErr: "cannot add user: duplicate username or external_id",
}, {
	about: "insert agent user",
	identity: &mongodoc.Identity{
		Username:   "agent",
		ExternalID: "",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
		},
		Owner:      "owner",
		PublicKeys: []mongodoc.PublicKey{{Key: []byte("0000000000000000000000000000000")}},
	},
}, {
	about: "duplicate agent",
	identity: &mongodoc.Identity{
		Username:   "existing-agent",
		ExternalID: "",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
		},
		Owner: "another owner",
		PublicKeys: []mongodoc.PublicKey{
			{Key: []byte("0000000000000000000000000000000")},
			{Key: []byte("1111111111111111111111111111111")},
		},
	},
	expectErr: "cannot add user: duplicate username or external_id",
}, {
	about: "not fully specified",
	identity: &mongodoc.Identity{
		Username:   "existing-agent",
		ExternalID: "",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
		},
		Owner: "",
		PublicKeys: []mongodoc.PublicKey{
			{Key: []byte("0000000000000000000000000000000")},
			{Key: []byte("1111111111111111111111111111111")},
		},
	},
	expectErr: "no external_id or owner specified",
}, {
	about: "external_id and owner",
	identity: &mongodoc.Identity{
		Username:   "existing-agent",
		ExternalID: "http://example.com/existing",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
		},
		Owner: "owner",
		PublicKeys: []mongodoc.PublicKey{
			{Key: []byte("0000000000000000000000000000000")},
			{Key: []byte("1111111111111111111111111111111")},
		},
	},
	expectErr: "both external_id and owner specified",
}}

func (s *storeSuite) TestUpsertIdentity(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Add existing interactive user.
	err := store.UpsertIdentity(&mongodoc.Identity{
		Username:   "existing",
		ExternalID: "http://example.com/existing",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
		},
	}, nil)
	c.Assert(err, gc.IsNil)

	// Add existing agent user
	err = store.UpsertIdentity(&mongodoc.Identity{
		Username: "existing-agent",
		Email:    "existing@example.com",
		FullName: "Existing User",
		Groups: []string{
			"test",
		},
		Owner:      "owner",
		PublicKeys: []mongodoc.PublicKey{{Key: []byte("00000000000000000000000000000000")}},
	}, nil)
	c.Assert(err, gc.IsNil)

	for i, test := range insertIdentityTests {
		c.Logf("%d: %s", i, test.about)
		err := store.UpsertIdentity(test.identity, nil)
		if test.expectErr != "" {
			c.Assert(err, gc.ErrorMatches, test.expectErr)
			continue
		}
		c.Assert(err, gc.IsNil)
		var doc mongodoc.Identity
		err = store.DB.Identities().Find(bson.D{{"username", test.identity.Username}}).One(&doc)
		c.Assert(err, gc.IsNil)
		c.Assert(&doc, jc.DeepEquals, test.identity)
	}
}

func (s *storeSuite) TestUpsertIdentityEmptyUserInformation(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)
	id := &mongodoc.Identity{
		Username:   "test",
		ExternalID: "http://example.com/test",
	}
	err := store.UpsertIdentity(id, nil)
	c.Assert(err, gc.IsNil)
	var doc map[string]interface{}
	err = store.DB.Identities().Find(bson.D{{"username", "test"}}).One(&doc)
	c.Assert(err, gc.IsNil)
	c.Assert(doc, jc.DeepEquals, map[string]interface{}{
		"_id":         id.UUID,
		"username":    "test",
		"external_id": "http://example.com/test",
		"email":       "",
		"gravatarid":  "",
		"fullname":    "",
	})
}

func (s *storeSuite) TestUpsertIdentityWithOnInsert(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)
	id := &mongodoc.Identity{
		Username:   "test",
		ExternalID: "http://example.com/test",
	}
	err := store.UpsertIdentity(id, bson.D{{"groups", []string{"test1", "test2"}}})
	c.Assert(err, gc.IsNil)
	var doc map[string]interface{}
	err = store.DB.Identities().Find(bson.D{{"username", "test"}}).One(&doc)
	c.Assert(err, gc.IsNil)
	c.Assert(doc, jc.DeepEquals, map[string]interface{}{
		"_id":         id.UUID,
		"username":    "test",
		"external_id": "http://example.com/test",
		"email":       "",
		"gravatarid":  "",
		"fullname":    "",
		"groups":      []interface{}{"test1", "test2"},
	})
}

func (s *storeSuite) TestUpsertIdentityDedupeGroups(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Add interactive user.
	id := &mongodoc.Identity{
		Username:   "test",
		ExternalID: "http://example.com/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
			"test2",
			"test2",
		},
	}
	err := store.UpsertIdentity(id, nil)
	c.Assert(err, gc.IsNil)

	expect := &mongodoc.Identity{
		Username:   "test",
		UUID:       id.UUID,
		ExternalID: "http://example.com/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
			"test2",
		},
	}

	var doc mongodoc.Identity
	err = store.DB.Identities().Find(bson.D{{"username", "test"}}).One(&doc)
	c.Assert(err, gc.IsNil)
	c.Assert(&doc, jc.DeepEquals, expect)
}

var updateGroupsIdentityTests = []struct {
	about     string
	identity  *mongodoc.Identity
	expectErr string
}{{
	about: "update interactive user",
	identity: &mongodoc.Identity{
		Username:   "existing",
		ExternalID: "http://example.com/existing",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
			"test2",
		},
	},
}, {
	about: "update existing agent",
	identity: &mongodoc.Identity{
		Username:   "existing-agent",
		ExternalID: "",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
			"test2",
		},
		Owner: "owner",
		PublicKeys: []mongodoc.PublicKey{
			{Key: []byte("0000000000000000000000000000000")},
		},
	},
}, {
	about: "update a non existing user",
	identity: &mongodoc.Identity{
		Username:   "non-existing-agent",
		ExternalID: "",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
			"test2",
		},
		Owner: "owner",
		PublicKeys: []mongodoc.PublicKey{
			{Key: []byte("0000000000000000000000000000000")},
		},
	},
	expectErr: "user \"non-existing-agent\" not found: not found",
}}

func (s *storeSuite) TestUpdateGroups(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Add existing interactive user.
	err := store.UpsertIdentity(&mongodoc.Identity{
		Username:   "existing",
		ExternalID: "http://example.com/existing",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
		},
	}, nil)
	c.Assert(err, gc.IsNil)

	// Add existing agent user
	err = store.UpsertIdentity(&mongodoc.Identity{
		Username: "existing-agent",
		Email:    "existing@example.com",
		FullName: "Existing User",
		Groups: []string{
			"test",
		},
		Owner:      "owner",
		PublicKeys: []mongodoc.PublicKey{{Key: []byte("0000000000000000000000000000000")}},
	}, nil)
	c.Assert(err, gc.IsNil)

	for i, test := range updateGroupsIdentityTests {
		c.Logf("%d: %s", i, test.about)
		err := store.SetGroups(test.identity.Username, test.identity.Groups)
		if test.expectErr != "" {
			c.Assert(err, gc.ErrorMatches, test.expectErr)
			continue
		}
		c.Assert(err, gc.IsNil)
		doc, err := store.GetIdentity(params.Username(test.identity.Username))
		c.Assert(err, gc.IsNil)
		doc.UUID = ""
		c.Assert(doc, jc.DeepEquals, test.identity)
	}
}

var updatePublicKeysIdentityTests = []struct {
	about     string
	identity  *mongodoc.Identity
	expectErr string
}{{
	about: "update existing agent",
	identity: &mongodoc.Identity{
		Username:   "existing-agent",
		ExternalID: "",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
			"test2",
		},
		Owner: "owner",
		PublicKeys: []mongodoc.PublicKey{
			{Key: []byte("0000000000000000000000000000000")},
			{Key: []byte("1111111111111111111111111111111")},
		},
	},
}, {
	about: "update a non existing agent",
	identity: &mongodoc.Identity{
		Username:   "non-existing-agent",
		ExternalID: "",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
			"test2",
		},
		Owner: "owner",
		PublicKeys: []mongodoc.PublicKey{
			{Key: []byte("0000000000000000000000000000000")},
		},
	},
	expectErr: "user \"non-existing-agent\" not found: not found",
}}

func (s *storeSuite) TestUpdatePublicKeys(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Add existing agent user
	err := store.UpsertIdentity(&mongodoc.Identity{
		Username:   "existing-agent",
		ExternalID: "",
		Email:      "existing@example.com",
		FullName:   "Existing User",
		Groups: []string{
			"test",
			"test2",
		},
		Owner: "owner",
		PublicKeys: []mongodoc.PublicKey{
			{Key: []byte("0000000000000000000000000000000")},
		},
	}, nil)
	c.Assert(err, gc.IsNil)

	for i, test := range updatePublicKeysIdentityTests {
		c.Logf("%d: %s", i, test.about)
		err := store.SetPublicKeys(test.identity.Username, test.identity.PublicKeys)
		if test.expectErr != "" {
			c.Assert(err, gc.ErrorMatches, test.expectErr)
			continue
		}
		c.Assert(err, gc.IsNil)
		doc, err := store.GetIdentity(params.Username(test.identity.Username))
		c.Assert(err, gc.IsNil)
		doc.UUID = ""
		c.Assert(doc, jc.DeepEquals, test.identity)
	}
}

func (s *storeSuite) TestSetGroupsDedupeGroups(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Add interactive user.
	id := &mongodoc.Identity{
		Username:   "test",
		ExternalID: "http://example.com/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
		},
	}
	err := store.UpsertIdentity(id, nil)
	c.Assert(err, gc.IsNil)
	err = store.SetGroups(id.Username, []string{"test", "test2", "test2"})
	c.Assert(err, gc.IsNil)

	expect := &mongodoc.Identity{
		Username:   "test",
		UUID:       id.UUID,
		ExternalID: "http://example.com/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
			"test2",
		},
	}

	var doc mongodoc.Identity
	err = store.DB.Identities().Find(bson.D{{"username", "test"}}).One(&doc)
	c.Assert(err, gc.IsNil)
	c.Assert(&doc, jc.DeepEquals, expect)
}

func (s *storeSuite) TestCollections(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)
	colls := store.DB.Collections()
	names, err := store.DB.CollectionNames()
	c.Assert(err, gc.IsNil)

	// Some collections don't have indexes so they are created only when used.
	createdOnUse := map[string]bool{
		"identity_providers": true,
	}

	// Check that all collections mentioned are actually created.
	for _, coll := range colls {
		found := false
		for _, name := range names {
			if name == coll.Name || createdOnUse[coll.Name] {
				found = true
			}
		}
		if !found {
			c.Errorf("collection %q not created", coll.Name)
		}
	}

	// Check that all created collections are mentioned in Collections.
	for _, name := range names {
		if name == "system.indexes" || name == "managedStoredResources" || name == "macaroons" {
			continue
		}
		found := false
		for _, coll := range colls {
			if coll.Name == name {
				found = true
			}
		}
		if !found {
			c.Errorf("extra collection %q found", name)
		}
	}
}

func (s *storeSuite) TestGetIdentity(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Add an identity to the store.
	err := store.UpsertIdentity(&mongodoc.Identity{
		Username:   "test",
		ExternalID: "http://example.com/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
		},
	}, nil)
	c.Assert(err, gc.IsNil)

	// Get the identity from the store
	id, err := store.GetIdentity(params.Username("test"))
	c.Assert(err, gc.IsNil)
	c.Assert(id.Username, gc.Equals, "test")
	c.Assert(id.ExternalID, gc.Equals, "http://example.com/test")
	c.Assert(id.Email, gc.Equals, "test@example.com")
	c.Assert(id.FullName, gc.Equals, "Test User")
	c.Assert(id.Groups, gc.DeepEquals, []string{"test"})

	// Get an identity not in the store
	id, err = store.GetIdentity(params.Username("noone"))
	c.Assert(id, gc.IsNil)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
}

func (s *storeSuite) TestUpdateIdentity(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Add an identity to the store.
	err := store.UpsertIdentity(&mongodoc.Identity{
		Username:   "test",
		ExternalID: "http://example.com/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
		},
	}, nil)
	c.Assert(err, gc.IsNil)

	// Update the identity in the store
	err = store.UpdateIdentity(params.Username("test"), bson.D{{"$set", bson.D{{"extrainfo.foo", []byte("true")}}}})
	c.Assert(err, gc.IsNil)
	id, err := store.GetIdentity(params.Username("test"))
	c.Assert(err, gc.IsNil)
	c.Assert(id.Username, gc.Equals, "test")
	c.Assert(id.ExternalID, gc.Equals, "http://example.com/test")
	c.Assert(id.Email, gc.Equals, "test@example.com")
	c.Assert(id.FullName, gc.Equals, "Test User")
	c.Assert(id.Groups, gc.DeepEquals, []string{"test"})
	c.Assert(id.ExtraInfo, gc.DeepEquals, map[string][]byte{"foo": []byte("true")})

	// Update an identity not in the store.
	err = store.UpdateIdentity(params.Username("noone"), bson.D{{"$set", bson.D{{"extrainfo.foo", []byte("false")}}}})
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
}

func (s *storeSuite) TestUpdateGroupsDoesntEraseSSHKeys(c *gc.C) {
	store := s.pool.GetNoLimit()
	defer s.pool.Put(store)

	// Add an identity to the store.
	err := store.UpsertIdentity(&mongodoc.Identity{
		Username:   "test",
		ExternalID: "http://example.com/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
		},
		SSHKeys: []string{"345ADASD34", "6745SDADSA"},
	}, nil)
	c.Assert(err, gc.IsNil)

	id, err := store.GetIdentity(params.Username("test"))
	c.Assert(id.SSHKeys, gc.DeepEquals, []string{"345ADASD34", "6745SDADSA"})

	err = store.SetGroups("test", []string{"test", "test2"})
	c.Assert(err, gc.IsNil)

	id, err = store.GetIdentity(params.Username("test"))
	c.Assert(err, gc.IsNil)
	c.Assert(id.SSHKeys, gc.DeepEquals, []string{"345ADASD34", "6745SDADSA"})
	c.Assert(id.Groups, gc.DeepEquals, []string{"test", "test2"})
}

func (s *storeSuite) TestRetrieveLaunchpadGroups(c *gc.C) {
	var lp *httptest.Server
	lp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.Logf("path: %s", r.URL.Path)
		switch r.URL.Path {
		case "/people":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"name": "test", "super_teams_collection_link": "%s/test/super_teams"}`, lp.URL)
		case "/test/super_teams":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"total_size":3,"start":0,"entries": [{"name": "test1"},{"name":"test2"}]}`)
		}
	}))
	defer lp.Close()
	pool, err := store.NewPool(
		s.Session.Copy().DB("store-launchpad-tests"),
		store.StoreParams{
			Launchpad:   lpad.APIBase(lp.URL),
			PrivateAddr: "localhost",
		},
	)
	c.Assert(err, gc.IsNil)
	defer pool.Close()
	store := pool.GetNoLimit()
	defer pool.Put(store)

	// Add an identity to the store.
	err = store.UpsertIdentity(&mongodoc.Identity{
		Username:   "test",
		ExternalID: "https://login.ubuntu.com/+id/test",
		Email:      "test@example.com",
		FullName:   "Test User",
		Groups: []string{
			"test",
		},
	}, nil)
	c.Assert(err, gc.IsNil)

	// Update group from an identity to be fetched from launchpad.
	groups, err := store.GetLaunchpadGroups("https://login.ubuntu.com/+id/test")
	c.Assert(err, gc.IsNil)
	groups = append([]string{"test"}, groups...)
	err = store.SetGroups("test", groups)
	c.Assert(err, gc.IsNil)

	// Get the identity from the store
	id, err := store.GetIdentity(params.Username("test"))
	c.Assert(err, gc.IsNil)
	c.Assert(id.Username, gc.Equals, "test")
	c.Assert(id.ExternalID, gc.Equals, "https://login.ubuntu.com/+id/test")
	c.Assert(id.Email, gc.Equals, "test@example.com")
	c.Assert(id.FullName, gc.Equals, "Test User")
	c.Assert(id.Groups, gc.DeepEquals, []string{"test", "test1", "test2"})
}

func (s *storeSuite) TestGetStoreFromPool(c *gc.C) {
	p, err := store.NewPool(s.Session.Copy().DB("store-launchpad-tests"),
		store.StoreParams{
			MaxMgoSessions: 2,
			PrivateAddr:    "localhost",
		},
	)
	c.Assert(err, gc.IsNil)
	defer p.Close()
	s1, err := p.Get()
	c.Assert(err, gc.IsNil)
	s2, err := p.Get()
	c.Assert(err, gc.IsNil)
	defer p.Put(s2)
	p.Put(s1)
	s3, err := p.Get()
	c.Assert(err, gc.IsNil)
	defer p.Put(s3)
	c.Assert(s3.DB.Database.Session, gc.Equals, s1.DB.Database.Session)
}

func (s *storeSuite) TestGetStoreFromPoolLimit(c *gc.C) {
	p, err := store.NewPool(s.Session.Copy().DB("store-launchpad-tests"),
		store.StoreParams{
			MaxMgoSessions: 1,
			RequestTimeout: 100 * time.Millisecond,
			PrivateAddr:    "localhost",
		},
	)
	c.Assert(err, gc.IsNil)
	defer p.Close()
	s1, err := p.Get()
	c.Assert(err, gc.IsNil)
	defer p.Put(s1)
	_, err = p.Get()
	c.Assert(err, gc.ErrorMatches, "too many mongo sessions in use: pool limit exceeded")
}

func (s *storeSuite) TestGetStoreFromPoolPutBeforeTimeout(c *gc.C) {
	p, err := store.NewPool(s.Session.Copy().DB("store-launchpad-tests"),
		store.StoreParams{
			MaxMgoSessions: 1,
			RequestTimeout: time.Second,
			PrivateAddr:    "localhost",
		},
	)
	c.Assert(err, gc.IsNil)
	defer p.Close()
	s1, err := p.Get()
	c.Assert(err, gc.IsNil)
	s1Session := s1.DB.Session
	go func() {
		time.Sleep(500 * time.Millisecond)
		p.Put(s1)
	}()
	s2, err := p.Get()
	c.Assert(err, gc.IsNil)
	defer p.Put(s2)
	c.Assert(s2.DB.Session, gc.Equals, s1Session)
}

type gauge struct {
	sync.Mutex
	prometheus.Gauge

	value int
}

func (g *gauge) Inc() {
	g.Lock()
	g.value++
	g.Unlock()
}

func (g *gauge) Dec() {
	g.Lock()
	g.value--
	g.Unlock()
}

func (g *gauge) GetValue() int {
	g.Lock()
	defer g.Unlock()
	return g.value
}

type monitoredPoolSuite struct {
	gauge *gauge
	pool  store.LimitPool
}

type nopCloser struct{}

func (*nopCloser) Close() {}

var _ = gc.Suite(&monitoredPoolSuite{})

func (s *monitoredPoolSuite) SetUpTest(c *gc.C) {
	s.gauge = &gauge{}
	s.pool = store.NewMonitoredPool(s.gauge, 3, func() limitpool.Item { return &nopCloser{} })
}

func (s *monitoredPoolSuite) TestGetIncrements(c *gc.C) {
	c.Assert(s.gauge.GetValue(), gc.Equals, 0)
	i, err := s.pool.Get(time.Second)
	c.Assert(err, gc.IsNil)
	c.Assert(s.gauge.GetValue(), gc.Equals, 0)
	s.pool.Put(i)
	c.Assert(s.gauge.GetValue(), gc.Equals, 1)
}

func (s *monitoredPoolSuite) TestGetNoLimitIncrements(c *gc.C) {
	c.Assert(s.gauge.GetValue(), gc.Equals, 0)
	i := s.pool.GetNoLimit()
	c.Assert(s.gauge.GetValue(), gc.Equals, 0)
	s.pool.Put(i)
	c.Assert(s.gauge.GetValue(), gc.Equals, 1)
}

type collectionMonitorSuite struct{}

var _ = gc.Suite(&collectionMonitorSuite{})

type mockCounter struct {
	value int
}

func (c *mockCounter) Count() (int, error) {
	return c.value, nil
}

func (s *collectionMonitorSuite) TestMonitorCollect(c *gc.C) {
	cnt := &mockCounter{value: 5}
	coll := store.NewCollectionMonitor(map[string]store.Counter{
		"sample": cnt,
	})
	ch := make(chan prometheus.Metric, 1)
	coll.Collect(ch)
	var m prometheus.Metric
	select {
	case m = <-ch:
	default:
		c.Error("no metric received from collector")
	}
	var value prom.Metric
	m.Write(&value)
	c.Assert(value.GetGauge().GetValue(), gc.Equals, 5.0)
}
