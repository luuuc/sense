using System;
using System.Collections.Generic;

namespace MyApp {
    public class Animal {
        public Animal(string name) {
        }

        public void Speak() {
            Console.WriteLine("hello");
        }
    }

    public class Dog : Animal {
        public Dog(string name) : base(name) {
        }

        public void Speak() {
            Console.WriteLine("woof");
        }
    }

    public interface IGroomable {
        void Groom();
    }

    public struct Point {
        public int X;
        public int Y;
    }

    public enum Color {
        Red, Green, Blue
    }
}
