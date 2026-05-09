class TopicCreator
  def create; end
end

class Cache
  def fetch; end
end

class PostCreator
  def initialize
    @topic_creator = TopicCreator.new
    @cache ||= Cache.new
  end

  def create_topic
    @topic_creator.create
    @cache.fetch
    @unknown.create
  end
end
