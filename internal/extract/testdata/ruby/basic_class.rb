class User
  VERSION = "1.0"

  def initialize(email)
    @email = email
  end

  def email
    @email
  end

  def self.find(id)
    new
  end
end
