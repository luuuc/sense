module WorkPackages
  class CreateService
    def call
      true
    end
  end
end

class WorkPackagesController
  def index
    true
  end
end

class UserMailer
  def welcome
    true
  end
end

class UserError
  def message
    "error"
  end
end
