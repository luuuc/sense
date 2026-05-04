class DynamicProxy
  def method_missing(name, *args)
    target.send(name, *args)
  end

  def respond_to_missing?(name, include_private = false)
    target.respond_to?(name, include_private)
  end

  def target
    @target
  end
end

module Configurable
  def self.configure
    yield self
  end
end

class PluginLoader
  REGISTRY = {}

  def self.register(name, klass)
    REGISTRY[name] = klass
  end

  def self.load_all
    REGISTRY.each_value do |klass|
      klass.new.activate
    end
  end
end
