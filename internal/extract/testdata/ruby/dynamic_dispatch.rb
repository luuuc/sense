class Dispatcher
  def dynamic
    send(:foo)
    public_send(:bar)
    __send__("baz")
    send(other)
  end

  def bare
    invisible
  end
end
