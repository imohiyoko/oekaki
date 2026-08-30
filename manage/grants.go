package manage

import (
	"path/filepath"
	"sort"
	"strings"
)

const grantsFile = "grants.json"

// Grants is who holds which roles.
//
// This is state, not configuration. Which roles exist and what each one may do
// is written by hand and shipped with the deployment; who holds one changes
// while the thing is running, by somebody clicking. Keeping the two in one
// file is what made the Python this replaces need a rule about stripping a
// field before saving, and rules like that are how a promise quietly stops
// being kept.
func (s *Store) Grants() (map[string][]string, error) {
	out := map[string][]string{}
	if err := readJSON(filepath.Join(s.root, grantsFile), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Grant gives a subject a set of roles, replacing what they had.
//
// known is the roles that exist. A grant naming something else is refused
// here, because at evaluation time it is simply no permissions — which looks
// exactly like the grant having worked and the person having no access for
// some other reason.
func (s *Store) Grant(subject string, roles []string, who Actor, known []string) error {
	if !strings.Contains(subject, ":") {
		return refuse("%q needs the provider that named it, as provider:name", subject)
	}
	have := make(map[string]bool, len(known))
	for _, r := range known {
		have[r] = true
	}
	var unknown []string
	for _, r := range roles {
		if !have[r] {
			unknown = append(unknown, r)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return refuse("no such role: %s", strings.Join(unknown, ", "))
	}

	all, err := s.Grants()
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		return s.Revoke(subject, who)
	}
	sorted := append([]string(nil), roles...)
	sort.Strings(sorted)
	all[subject] = sorted

	body, err := marshal(all)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(s.root, grantsFile), body); err != nil {
		return err
	}
	_, err = s.Record(who, ActionGrant, subject, map[string]any{"roles": sorted})
	return err
}

// Revoke takes every role away from a subject. Revoking from somebody who had
// none is not a change and is not recorded.
func (s *Store) Revoke(subject string, who Actor) error {
	all, err := s.Grants()
	if err != nil {
		return err
	}
	if _, ok := all[subject]; !ok {
		return nil
	}
	delete(all, subject)
	body, err := marshal(all)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(s.root, grantsFile), body); err != nil {
		return err
	}
	_, err = s.Record(who, ActionRevoke, subject, nil)
	return err
}
