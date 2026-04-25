import java.util.List;

public class Animal {
    private String name;

    public Animal(String name) {
        this.name = name;
    }

    public void speak() {
        System.out.println("hello");
    }
}

class Dog extends Animal implements Groomable {
    public Dog(String name) {
        super(name);
    }

    public void speak() {
        this.wag();
        System.out.println("woof");
    }
}

interface Groomable {
    void groom();
}

enum Color {
    RED, GREEN, BLUE
}
