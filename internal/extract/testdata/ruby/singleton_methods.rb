class Configuration
  DEFAULT_TIMEOUT = 30

  def self.from_env
    new
  end

  def self.reset!
    @instance = nil
  end

  def timeout
    DEFAULT_TIMEOUT
  end
end

module Helpers
  def self.format_name(first, last)
    "#{first} #{last}"
  end
end
