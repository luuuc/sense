import scala.collection.mutable.ListBuffer

class Animal(val name: String) {
  def speak(): Unit = {
    println("hello")
  }
}

class Dog(name: String) extends Animal(name) {
  override def speak(): Unit = {
    println("woof")
  }
}

trait Groomable {
  def groom(): Unit
}

object Singleton {
  def doWork(): Unit = {
    println("working")
  }
}
