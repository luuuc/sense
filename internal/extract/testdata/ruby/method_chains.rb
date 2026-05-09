# Ruby method-chain resolution fixture.
# Covers multi-hop chains via return-type map inference.

class Caller
  # Basic two-hop chain: local var → method → method
  def basic_chain
    factory = Factory.new
    factory.builder.create
  end

  # Self receiver chain: self → method → method
  def self_chain
    self.builder.create
  end

  # Three-hop chain
  def three_hop_chain
    factory = Factory.new
    factory.builder.config.load
  end

  # Unresolved chain: middle method has no known return type
  def unresolved_chain
    factory = Factory.new
    factory.unknown.create
  end

  # Nested chain via another local
  def nested_chain
    factory = Factory.new
    builder = factory.builder
    builder.create
  end
end

class Factory
  def builder
    Builder.new
  end

  def unknown
    Something.new
  end
end

class Builder
  def create; end

  def config
    Config.new
  end
end

class Config
  def load; end
end
