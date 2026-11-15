package mongodb

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Config is the connection form: a connection string, or the fields.
type Config struct {
	URI        string `json:"uri"`        // mongodb://… or mongodb+srv://…; when set, only Database below still counts
	Host       string `json:"host"`       // e.g. "localhost"
	Port       int    `json:"port"`       // e.g. 27017
	User       string `json:"user"`       // "" connects without signing in
	Password   string `json:"password"`   // kept in the saved connection, like Postgres's
	Database   string `json:"database"`   // the database to open; "" uses the connection string's, else the first one there is
	AuthSource string `json:"authSource"` // the database the user was created in; "" is admin
	TLS        bool   `json:"tls"`        // talk to the server over TLS (Atlas and most hosted servers need it)
}

var defaultConfig = Config{Host: "localhost", Port: 27017}

// connectTimeout limits finding a server to talk to, so a wrong address
// fails in seconds rather than the driver's 30.
const connectTimeout = 5 * time.Second

func (c Config) validate() error {
	if uri := strings.TrimSpace(c.URI); uri != "" {
		if !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://") {
			return errors.New("A connection string starts with mongodb:// or mongodb+srv://.")
		}
	} else {
		if strings.TrimSpace(c.Host) == "" {
			return errors.New("host is required")
		}
		if c.Port < 1 || c.Port > 65535 {
			return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
		}
	}
	if c.Database != "" {
		return validDatabase(c.Database)
	}
	return nil
}

// validDatabase refuses names MongoDB doesn't allow for a database.
func validDatabase(name string) error {
	if strings.ContainsAny(name, "/\\. \"$\x00") || len(name) > 63 {
		return fmt.Errorf("“%s” can’t be a MongoDB database name: no spaces, dots, slashes, quotes or $, and at most 63 characters.", name)
	}
	return nil
}

// clientOptions are the driver's options for c.
func (c Config) clientOptions() *options.ClientOptions {
	opts := options.Client()
	if uri := strings.TrimSpace(c.URI); uri != "" {
		opts.ApplyURI(uri)
	} else {
		opts.SetHosts([]string{net.JoinHostPort(strings.TrimSpace(c.Host), strconv.Itoa(c.Port))})
		if c.User != "" {
			source := c.AuthSource
			if source == "" {
				source = "admin"
			}
			opts.SetAuth(options.Credential{Username: c.User, Password: c.Password, AuthSource: source})
		}
		if c.TLS {
			opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
		}
	}
	// Labels SpyDB's connections in the server's logs and currentOp.
	opts.SetAppName("SpyDB")
	opts.SetConnectTimeout(connectTimeout)
	opts.SetServerSelectionTimeout(connectTimeout)
	return opts
}

// target is what the connection reaches, for labels and Recent: its hosts,
// user and database, from the fields or the connection string (read here
// without the driver, which would look up a mongodb+srv:// name in DNS).
func (c Config) target() (hosts, user, database string) {
	if strings.TrimSpace(c.URI) == "" {
		return net.JoinHostPort(strings.TrimSpace(c.Host), strconv.Itoa(c.Port)), c.User, c.Database
	}
	hosts, user, database = uriParts(c.URI)
	if c.Database != "" {
		database = c.Database
	}
	return hosts, user, database
}

// uriParts reads a connection string's hosts, user and database:
// "mongodb+srv://ana:pw@cluster0.ab12.mongodb.net/shop?retryWrites=true"
// gives "cluster0.ab12.mongodb.net", "ana", "shop".
func uriParts(uri string) (hosts, user, database string) {
	rest := strings.TrimSpace(uri)
	if _, after, ok := strings.Cut(rest, "://"); ok {
		rest = after
	}
	rest, _, _ = strings.Cut(rest, "?")
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		name, _, _ := strings.Cut(rest[:i], ":")
		if u, err := url.PathUnescape(name); err == nil {
			user = u
		} else {
			user = name
		}
		rest = rest[i+1:]
	}
	hosts, database, _ = strings.Cut(rest, "/")
	if d, err := url.PathUnescape(database); err == nil {
		database = d
	}
	return hosts, user, database
}
