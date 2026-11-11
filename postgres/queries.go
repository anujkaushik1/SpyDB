package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/anujkaushik1/SpyDB/appdata"
)

// SavedQuery is SQL the user saved by name from a query tab. Saved queries
// are shared by every connection; Database and Host only label where each one
// was last saved.
type SavedQuery struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SQL       string `json:"sql"`
	Database  string `json:"database"`  // e.g. "cashier"; "" if saved while disconnected
	Host      string `json:"host"`      // for the label's tooltip
	UpdatedAt int64  `json:"updatedAt"` // Unix milliseconds
}

type queriesDoc struct {
	Queries []SavedQuery `json:"queries"`
}

// queriesFile is queries.json in SpyDB's settings folder, next to connections.json.
func queriesFile() (string, error) {
	return appdata.Path("queries.json")
}

// SavedQueries returns every saved query, most recently saved first.
func (s *DBService) SavedQueries() ([]SavedQuery, error) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	return loadQueries()
}

// SaveQuery creates a saved query (empty ID) or replaces the SQL and name of
// the one with that ID. Either way it records the time and the database
// you're connected to, and returns the saved query.
func (s *DBService) SaveQuery(q SavedQuery) (SavedQuery, error) {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		return SavedQuery{}, errors.New("Give the query a name.")
	}
	if strings.TrimSpace(q.SQL) == "" {
		return SavedQuery{}, errors.New("There's no SQL to save.")
	}
	s.mu.Lock()
	info := s.info
	s.mu.Unlock()
	q.Database, q.Host = "", ""
	if info != nil {
		q.Database, q.Host = info.Database, info.Host
	}
	q.UpdatedAt = time.Now().UnixMilli()

	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	queries, err := loadQueries()
	if err != nil {
		return SavedQuery{}, err
	}
	if q.ID == "" {
		q.ID = newID()
		queries = append(queries, q)
	} else {
		i := indexOfQuery(queries, q.ID)
		if i < 0 {
			return SavedQuery{}, errors.New("This saved query was deleted. Save it again to keep it.")
		}
		queries[i] = q
	}
	return q, writeQueries(queries)
}

// RenameQuery changes only a saved query's name.
func (s *DBService) RenameQuery(id, name string) (SavedQuery, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SavedQuery{}, errors.New("Give the query a name.")
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	queries, err := loadQueries()
	if err != nil {
		return SavedQuery{}, err
	}
	i := indexOfQuery(queries, id)
	if i < 0 {
		return SavedQuery{}, errors.New("This saved query was deleted.")
	}
	queries[i].Name = name
	return queries[i], writeQueries(queries)
}

// DeleteQuery removes a saved query; deleting one that's already gone is fine.
func (s *DBService) DeleteQuery(id string) error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	queries, err := loadQueries()
	if err != nil {
		return err
	}
	i := indexOfQuery(queries, id)
	if i < 0 {
		return nil
	}
	return writeQueries(append(queries[:i], queries[i+1:]...))
}

func indexOfQuery(queries []SavedQuery, id string) int {
	for i, q := range queries {
		if q.ID == id {
			return i
		}
	}
	return -1
}

func loadQueries() ([]SavedQuery, error) {
	path, err := queriesFile()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []SavedQuery{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc queriesDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", path, err)
	}
	if doc.Queries == nil {
		doc.Queries = []SavedQuery{}
	}
	return doc.Queries, nil
}

// writeQueries stores the list newest first, readable only by the current
// user (queries can name tables and hold literal values).
func writeQueries(queries []SavedQuery) error {
	path, err := queriesFile()
	if err != nil {
		return err
	}
	sort.SliceStable(queries, func(a, b int) bool { return queries[a].UpdatedAt > queries[b].UpdatedAt })
	return appdata.WritePrivateJSON(path, queriesDoc{Queries: queries})
}
