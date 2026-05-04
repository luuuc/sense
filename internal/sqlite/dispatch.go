package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// DispatchMethodIDs finds methods that share a name with the given method
// but live on types connected through inherits edges. This covers both
// directions: if the method's parent is an interface, returns methods on
// implementing types; if the parent is a concrete type, returns methods on
// the interfaces it implements. Returns nil when the symbol has no parent
// or no dispatch equivalents exist.
func DispatchMethodIDs(ctx context.Context, db *sql.DB, symbolID int64) ([]int64, error) {
	var name string
	var parentID sql.NullInt64
	var parentKind sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT s.name, s.parent_id, p.kind
		 FROM sense_symbols s
		 LEFT JOIN sense_symbols p ON p.id = s.parent_id
		 WHERE s.id = ?`, symbolID,
	).Scan(&name, &parentID, &parentKind)
	if err != nil {
		return nil, fmt.Errorf("dispatch: lookup symbol %d: %w", symbolID, err)
	}
	if !parentID.Valid {
		return nil, nil
	}

	if parentKind.String == "interface" {
		return dispatchFromInterface(ctx, db, parentID.Int64, name)
	}
	return dispatchFromConcrete(ctx, db, parentID.Int64, name)
}

// dispatchFromInterface: parent is an interface → find methods with the
// same name on types that inherit from this interface.
func dispatchFromInterface(ctx context.Context, db *sql.DB, ifaceID int64, methodName string) ([]int64, error) {
	const q = `SELECT m.id
		FROM sense_edges e
		JOIN sense_symbols m ON m.parent_id = e.source_id AND m.name = ?
		WHERE e.target_id = ? AND e.kind = 'inherits' AND e.source_id IS NOT NULL`

	return collectIDs(ctx, db, q, methodName, ifaceID)
}

// dispatchFromConcrete: parent is a concrete type → find methods with the
// same name on interfaces this type inherits from.
func dispatchFromConcrete(ctx context.Context, db *sql.DB, typeID int64, methodName string) ([]int64, error) {
	const q = `SELECT m.id
		FROM sense_edges e
		JOIN sense_symbols iface ON e.target_id = iface.id AND iface.kind = 'interface'
		JOIN sense_symbols m ON m.parent_id = iface.id AND m.name = ?
		WHERE e.source_id = ? AND e.kind = 'inherits'`

	return collectIDs(ctx, db, q, methodName, typeID)
}

// InterfaceMethodKey identifies a method on an implementing type by its
// parent type ID and method name.
type InterfaceMethodKey struct {
	ParentID   int64
	MethodName string
}

// InterfaceAliveMethods finds interface methods that have callers, then maps
// those to all types that implement those interfaces. Returns a set of
// (implementor_parent_id, method_name) pairs. This is the bulk variant of
// DispatchMethodIDs, used by dead-code detection.
func InterfaceAliveMethods(ctx context.Context, db *sql.DB) (map[InterfaceMethodKey]struct{}, error) {
	const q = `SELECT impl.source_id, im.name
		FROM sense_symbols im
		JOIN sense_edges ie ON ie.target_id = im.id AND ie.kind = 'calls'
		JOIN sense_symbols iface ON im.parent_id = iface.id AND iface.kind = 'interface'
		JOIN sense_edges impl ON impl.target_id = iface.id AND impl.kind = 'inherits' AND impl.source_id IS NOT NULL
		GROUP BY impl.source_id, im.name`

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[InterfaceMethodKey]struct{})
	for rows.Next() {
		var parentID int64
		var methodName string
		if err := rows.Scan(&parentID, &methodName); err != nil {
			return nil, err
		}
		out[InterfaceMethodKey{ParentID: parentID, MethodName: methodName}] = struct{}{}
	}
	return out, rows.Err()
}

func collectIDs(ctx context.Context, db *sql.DB, q string, args ...any) ([]int64, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
