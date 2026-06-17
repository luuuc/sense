package scan_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// A model that invokes an acts_as_* plugin macro gains a synthesized composes
// edge to the collaborator class the macro wires in, even though the model never
// names that class. The macro establishes the link two hops away
// (model -> acts_as_attachable -> Attachment); the resolve phase stitches it into
// a direct model -> Attachment edge so blast/graph surface the model as a
// dependent of the collaborator (a grep-invisible relationship).
func TestMixinExpansionSynthesizesModelToCollaboratorEdge(t *testing.T) {
	root := t.TempDir()

	// The plugin macro: its body references the collaborator class, so the macro
	// method carries an edge to Attachment.
	writeFile(t, filepath.Join(root, "lib/plugins/acts_as_attachable.rb"), `
module Acts
  module Attachable
    def self.included(base)
      base.extend ClassMethods
    end
    module ClassMethods
      def acts_as_attachable(options = {})
        Attachment.table_name
      end
    end
  end
end
`)
	// The collaborator class.
	writeFile(t, filepath.Join(root, "app/models/attachment.rb"), `
class Attachment < ApplicationRecord
end
`)
	// A participant that invokes the macro with NO arguments (bare-identifier
	// form) and never names Attachment.
	writeFile(t, filepath.Join(root, "app/models/message.rb"), `
class Message < ApplicationRecord
  acts_as_attachable
end
`)
	// A participant that invokes the macro WITH arguments (call form).
	writeFile(t, filepath.Join(root, "app/models/news.rb"), `
class News < ApplicationRecord
  acts_as_attachable view_permission: :view_news
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	byQualified := map[string]model.Symbol{}
	for _, s := range all {
		byQualified[s.Qualified] = s
	}

	attachment, ok := byQualified["Attachment"]
	if !ok {
		t.Fatal("Attachment symbol not indexed")
	}

	for _, participant := range []string{"Message", "News"} {
		sym, ok := byQualified[participant]
		if !ok {
			t.Fatalf("%s symbol not indexed", participant)
		}
		full, err := a.ReadSymbol(ctx, sym.ID)
		if err != nil {
			t.Fatalf("ReadSymbol(%s): %v", participant, err)
		}
		found := false
		for _, e := range full.Outbound {
			if e.Target.ID == attachment.ID && e.Edge.Kind == model.EdgeComposes {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s has no synthesized composes edge to Attachment (mixin expansion failed)", participant)
		}
	}
}
