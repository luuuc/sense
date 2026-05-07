# Ruby call-pattern fixture: covers the main invocation shapes.
class Caller
  def explicit_receiver
    helper = Helper.new
    helper.greet
  end

  def implicit_self
    greet
  end

  def bare_method
    process
  end

  def class_method
    Helper.configure
  end

  def block_call
    items = List.new
    items.each do |item|
      item.process
    end
  end

  def chain_call
    factory = Factory.new
    factory.builder.create
  end
end

class Helper
  def greet; end
  def self.configure; end
end

class List
  def each; end
end

class Factory
  def builder
    Builder.new
  end
end

class Builder
  def create; end
end
