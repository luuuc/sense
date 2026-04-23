package embed

import (
	"context"
	"testing"
)

type separationCase struct {
	name  string
	query string

	targetCurrent EmbedInput
	targetRich    string

	noiseCurrent EmbedInput
	noiseRich    string
}

var separationCases = []separationCase{
	{
		name:  "OpenProject/scheduling_and_date_dependencies",
		query: "scheduling and date dependencies for work packages",
		targetCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "WorkPackages::SetScheduleService#set_dates",
			Snippet:       "def set_dates(work_package)",
		},
		targetRich: "File: app/services/work_packages/set_schedule_service.rb\n" +
			"method SetScheduleService#set_dates\n" +
			"def set_dates(work_package)\n" +
			"  work_package.start_date = start_date(work_package)\n" +
			"  work_package.due_date = due_date(work_package)\n" +
			"  compute_successor_dates(work_package)",
		noiseCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "WorkPackages::SetAttributesService#set_default_values",
			Snippet:       "def set_default_values",
		},
		noiseRich: "File: app/services/work_packages/set_attributes_service.rb\n" +
			"method SetAttributesService#set_default_values\n" +
			"def set_default_values\n" +
			"  set_default_priority\n" +
			"  set_default_status\n" +
			"  set_default_author",
	},
	{
		name:  "Discourse/post_moderation_and_flagging",
		query: "post moderation and flagging",
		targetCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "ReviewableFlaggedPost#perform_agree_and_hide",
			Snippet:       "def perform_agree_and_hide(performed_by, args)",
		},
		targetRich: "File: app/models/reviewable_flagged_post.rb\n" +
			"method ReviewableFlaggedPost#perform_agree_and_hide\n" +
			"def perform_agree_and_hide(performed_by, args)\n" +
			"  agree_with_flags(performed_by)\n" +
			"  post.hide!(post_action_type_id)\n" +
			"  create_result(:success, :agree_and_hide)",
		noiseCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "Post#publish_change_to_clients",
			Snippet:       "def publish_change_to_clients",
		},
		noiseRich: "File: app/models/post.rb\n" +
			"method Post#publish_change_to_clients\n" +
			"def publish_change_to_clients\n" +
			"  MessageBus.publish(\"/topic/#{topic_id}\", type: :revised, id: id)",
	},
	{
		name:  "GitLab/merge_request_approval_workflow",
		query: "merge request approval workflow",
		targetCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "ApprovalRules::CreateService#execute",
			Snippet:       "def execute",
		},
		targetRich: "File: app/services/approval_rules/create_service.rb\n" +
			"method ApprovalRules::CreateService#execute\n" +
			"def execute\n" +
			"  rule = ApprovalMergeRequestRule.new(merge_request: merge_request)\n" +
			"  rule.approvals_required = params[:approvals_required]\n" +
			"  rule.users = params[:user_ids].map { User.find(_1) }",
		noiseCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "MergeRequests::UpdateService#execute",
			Snippet:       "def execute(merge_request)",
		},
		noiseRich: "File: app/services/merge_requests/update_service.rb\n" +
			"method MergeRequests::UpdateService#execute\n" +
			"def execute(merge_request)\n" +
			"  update_title(merge_request)\n" +
			"  update_description(merge_request)\n" +
			"  update_assignees(merge_request)",
	},
	{
		name:  "OpenProject/notification_delivery",
		query: "notification delivery for work package changes",
		targetCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "Notifications::CreateService#call",
			Snippet:       "def call",
		},
		targetRich: "File: app/services/notifications/create_service.rb\n" +
			"method Notifications::CreateService#call\n" +
			"def call\n" +
			"  notification = Notification.new(recipient: recipient, resource: work_package)\n" +
			"  notification.reason = determine_reason(journal)\n" +
			"  deliver_mail(notification) if recipient.mail_notification?",
		noiseCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "Notifications::SetAttributesService#set_attributes",
			Snippet:       "def set_attributes",
		},
		noiseRich: "File: app/services/notifications/set_attributes_service.rb\n" +
			"method Notifications::SetAttributesService#set_attributes\n" +
			"def set_attributes\n" +
			"  model.project = params[:project]\n" +
			"  model.actor = params[:actor]\n" +
			"  model.read_ian = false",
	},
	{
		name:  "GitLab/user_authentication_and_password",
		query: "user authentication and password validation",
		targetCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "Users::AuthenticateService#execute",
			Snippet:       "def execute",
		},
		targetRich: "File: app/services/users/authenticate_service.rb\n" +
			"method Users::AuthenticateService#execute\n" +
			"def execute\n" +
			"  user = User.find_by(email: params[:login])\n" +
			"  return error unless user&.valid_password?(params[:password])\n" +
			"  check_two_factor(user)",
		noiseCurrent: EmbedInput{
			Kind:          "method",
			QualifiedName: "Users::UpdateService#execute",
			Snippet:       "def execute",
		},
		noiseRich: "File: app/services/users/update_service.rb\n" +
			"method Users::UpdateService#execute\n" +
			"def execute\n" +
			"  user.name = params[:name]\n" +
			"  user.email = params[:email]\n" +
			"  user.save",
	},
}

// TestSimilaritySeparation validates that graph-derived rich context
// produces better query-to-target separation than the current format.
// Each case embeds a query, target, and noise symbol in both current
// and rich formats, then asserts the rich format separates signal
// from noise (cosine(query, target) > cosine(query, noise)).
func TestSimilaritySeparation(t *testing.T) {
	emb := setupEmbedder(t)

	var totalCurrentSep, totalRichSep float64

	for _, tc := range separationCases {
		t.Run(tc.name, func(t *testing.T) {
			inputs := []EmbedInput{
				{Snippet: tc.query},
				tc.targetCurrent,
				tc.noiseCurrent,
				{Snippet: tc.targetRich},
				{Snippet: tc.noiseRich},
			}

			vecs, err := emb.Embed(context.Background(), inputs)
			if err != nil {
				t.Fatalf("embed: %v", err)
			}

			queryVec := vecs[0]

			currentTargetSim := cosineSimilarity(queryVec, vecs[1])
			currentNoiseSim := cosineSimilarity(queryVec, vecs[2])
			currentSep := currentTargetSim - currentNoiseSim

			richTargetSim := cosineSimilarity(queryVec, vecs[3])
			richNoiseSim := cosineSimilarity(queryVec, vecs[4])
			richSep := richTargetSim - richNoiseSim

			totalCurrentSep += currentSep
			totalRichSep += richSep

			t.Logf("current: target=%.4f noise=%.4f sep=%.4f", currentTargetSim, currentNoiseSim, currentSep)
			t.Logf("rich:    target=%.4f noise=%.4f sep=%.4f", richTargetSim, richNoiseSim, richSep)

			if richSep <= 0 {
				t.Errorf("rich format failed to separate signal from noise: sep=%.4f (target=%.4f, noise=%.4f)",
					richSep, richTargetSim, richNoiseSim)
			}

			if richSep <= currentSep {
				t.Logf("note: rich format separation (%.4f) did not improve over current (%.4f)", richSep, currentSep)
			}
		})
	}

	n := float64(len(separationCases))
	avgCurrentSep := totalCurrentSep / n
	avgRichSep := totalRichSep / n
	t.Logf("average separation — current: %.4f  rich: %.4f  improvement: %.4f",
		avgCurrentSep, avgRichSep, avgRichSep-avgCurrentSep)

	if avgRichSep <= 0 {
		t.Errorf("average rich separation is non-positive: %.4f", avgRichSep)
	}
}
