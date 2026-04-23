package search

import (
	"testing"
)

func TestApplyPathWeights(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantFactor string // "demoted", "boosted", or "neutral"
	}{
		// Migrations (all 3 repos)
		{"OpenProject migration", "db/migrate/20230101_add_scheduling_columns.rb", "demoted"},
		{"GitLab post_migrate", "db/post_migrate/20230101_backfill_approval_rules.rb", "demoted"},
		{"Discourse migration", "db/migrate/20230101_create_reviewable_scores.rb", "demoted"},

		// Scripts
		{"Discourse import script", "script/import_scripts/import_vanilla.rb", "demoted"},
		{"GitLab script", "scripts/generate_release_notes.rb", "demoted"},

		// Test/spec paths (nested — must use Contains, not HasPrefix)
		{"OpenProject spec", "spec/services/work_packages/set_schedule_service_spec.rb", "demoted"},
		{"Discourse plugin spec", "plugins/chat/spec/models/chat_message_spec.rb", "demoted"},
		{"GitLab nested spec", "ee/spec/services/approval_rules/create_service_spec.rb", "demoted"},
		{"Go test file", "internal/scan/scan_test.go", "demoted"},
		{"OpenProject test", "modules/costs/test/unit/cost_entry_test.rb", "demoted"},
		{"test directory", "app/services/test/helper.rb", "demoted"},

		// Root-level spec/ and test/ (no _spec/_test suffix)
		{"RSpec helper", "spec/support/moderation_helper.rb", "demoted"},
		{"RSpec shared context", "spec/shared_contexts/authenticated_user.rb", "demoted"},
		{"minitest helper", "test/helpers/moderation_helper.rb", "demoted"},
		{"minitest support", "test/support/factory.rb", "demoted"},

		// Mock/fixture paths
		{"mock directory", "spec/mock/fake_notifier.rb", "demoted"},
		{"mocks directory", "internal/mocks/embedder.go", "demoted"},
		{"fixture directory", "spec/fixtures/work_packages.yml", "demoted"},
		{"fixtures nested", "plugins/chat/spec/fixtures/uploads.yml", "demoted"},
		{"Go testdata", "internal/embed/testdata/vocab.txt", "demoted"},

		// Generated code
		{"generated protobuf", "app/generated/api_pb.rb", "demoted"},

		// Source directories (boosted)
		{"OpenProject app model", "app/models/work_package.rb", "boosted"},
		{"OpenProject app service", "app/services/work_packages/set_schedule_service.rb", "boosted"},
		{"Discourse app model", "app/models/reviewable_flagged_post.rb", "boosted"},
		{"GitLab app service", "app/services/approval_rules/create_service.rb", "boosted"},
		{"GitLab lib", "lib/gitlab/auth/ldap/adapter.rb", "boosted"},
		{"Go src", "src/main/server.go", "boosted"},

		// Neutral paths (must NOT false-positive)
		{"config file", "config/routes.rb", "neutral"},
		{"root file", "Gemfile", "neutral"},
		{"vendor", "vendor/gems/some_gem/lib/thing.rb", "neutral"},
		{"specification not spec", "app/models/specification/builder.rb", "boosted"},
		{"testing not test", "app/services/testing_utils/validator.rb", "boosted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []Result{{FileID: 1, Score: 1.0}}
			pathByID := map[int64]string{1: tt.path}

			applyPathWeights(results, pathByID)

			score := results[0].Score
			switch tt.wantFactor {
			case "demoted":
				if score >= 1.0 {
					t.Errorf("path %q should be demoted, got score %.2f", tt.path, score)
				}
			case "boosted":
				if score <= 1.0 {
					t.Errorf("path %q should be boosted, got score %.2f", tt.path, score)
				}
			case "neutral":
				if score != 1.0 {
					t.Errorf("path %q should be neutral (1.0), got score %.2f", tt.path, score)
				}
			}
		})
	}
}

func TestApplyPathWeightsPenaltyValues(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantMul float64
	}{
		{"migration gets 0.3", "db/migrate/001_create.rb", 0.3},
		{"spec gets 0.5", "app/services/spec/foo_spec.rb", 0.5},
		{"test file gets 0.5", "internal/scan/scan_test.go", 0.5},
		{"generated gets 0.4", "app/generated/schema.rb", 0.4},
		{"source gets 1.1", "app/models/user.rb", sourceBoost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []Result{{FileID: 1, Score: 1.0}}
			pathByID := map[int64]string{1: tt.path}

			applyPathWeights(results, pathByID)

			if diff := results[0].Score - tt.wantMul; diff > 0.001 || diff < -0.001 {
				t.Errorf("path %q: got score %.4f, want %.4f", tt.path, results[0].Score, tt.wantMul)
			}
		})
	}
}
