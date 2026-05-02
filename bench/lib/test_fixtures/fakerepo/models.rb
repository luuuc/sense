class TopicCreator
  def create(params)
    Topic.new(params)
  end

  def self.create(params)
    new.create(params)
  end
end

class Topic
  def initialize(params)
    @params = params
  end
end

module UnusedModule
  def self.noop
  end
end
