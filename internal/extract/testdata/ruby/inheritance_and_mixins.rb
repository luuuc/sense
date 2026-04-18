module Printable
  def print_self
    puts self
  end
end

class Base
  def hello
  end
end

class Child < Base
  include Printable
  extend Printable

  def greet
  end
end
