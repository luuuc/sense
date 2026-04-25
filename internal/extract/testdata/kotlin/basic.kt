import java.util.List

open class Animal(val name: String) {
    open fun speak() {
        println("hello")
    }
}

class Dog(name: String) : Animal(name) {
    override fun speak() {
        println("woof")
    }
}

interface Groomable {
    fun groom()
}

object Singleton {
    fun doWork() {
        println("working")
    }
}

fun main() {
    val d = Dog("Rex")
    d.speak()
}
