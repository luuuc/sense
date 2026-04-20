module Searchable
  extend ActiveSupport::Concern

  included do
    has_many :search_entries
    after_commit :reindex
  end

  def search
  end
end

module Auditable
  extend ActiveSupport::Concern

  included do
    belongs_to :auditor
    before_save :record_audit
    has_one :audit_log
  end

  def audit_trail
  end
end

# Negative case: module without ActiveSupport::Concern
# Regular include block should NOT get special treatment
module PlainModule
  def self.included(base)
    base.has_many :widgets
  end

  def helper
  end
end

# Negative case: a concern with class_methods block (not included)
module Cacheable
  extend ActiveSupport::Concern

  class_methods do
    def cache_key
    end
  end

  def expire_cache
  end
end
