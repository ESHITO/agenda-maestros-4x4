package booking

import "encoding/json"

// forkConds adds the fork's list predicates (Agenda Maestros 4x4) to where()'s: today the
// event-type set of ListFilter.EventTypeIDs, which the handler fills from ?area= (the
// team's Mentoría or Soporte types) and from ?event_type=<template slug> (the template and
// every mentor's copy of it). The ids travel as one JSON array (json_each), so the SQL text
// never depends on the input.
func (f ListFilter) forkConds(conds []string, args []any) ([]string, []any) {
	if f.EventTypeIDs == nil {
		return conds, args
	}
	idsJSON, _ := json.Marshal(f.EventTypeIDs)
	conds = append(conds, "bookings.event_type_id IN (SELECT value FROM json_each(?))")
	return conds, append(args, string(idsJSON))
}
