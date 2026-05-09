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
  prepend Enumerable

  def greet
  end
end
