# Bare method calls (no parens, no receiver) in statement position.
# Tree-sitter-ruby parses these as identifier nodes rather than call
# nodes. The extractor picks them up via emitBareIdentifierCalls.
class PostCreator
  def create
    create_topic
    save_post
    if valid?
      track_topic
    else
      rollback
    end
    begin
      finalize
    rescue
      handle_error
    ensure
      cleanup
    end
  end

  def create_topic
  end

  private

  def save_post
  end

  def track_topic
  end
end
