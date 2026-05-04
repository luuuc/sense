class Vehicle < ApplicationRecord
  self.table_name = "vehicles"
end

class Car < Vehicle
  def drive
    start_engine
    accelerate
  end
end

class Truck < Vehicle
  PAYLOAD_LIMIT = 10000

  def haul(cargo)
    cargo.load
  end
end

class ElectricCar < Car
  def charge
    battery.connect
  end
end
