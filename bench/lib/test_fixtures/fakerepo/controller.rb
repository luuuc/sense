class TopicController
  def index
    creator = TopicCreator.new
    topic = creator.create(title: "Hello")
    render topic
  end

  def render(obj)
    obj.to_s
  end
end
