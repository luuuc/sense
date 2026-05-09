class Service
  def symbol_callback
    callback = :after_save
    send(callback)
  end

  def string_action
    action = "index"
    send(action)
  end

  def public_send_callback
    callback = :process
    public_send(callback)
  end

  def no_pattern_match
    count = :something
    send(count)
  end

  def non_self_receiver
    callback = :after_save
    obj.send(callback)
  end

  def no_assignment
    send(callback)
  end
end
