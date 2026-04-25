#include <iostream>

namespace geometry {

class Shape {
public:
    virtual void draw() {
        std::cout << "shape" << std::endl;
    }
};

class Circle : public Shape {
public:
    void draw() {
        std::cout << "circle" << std::endl;
    }

    double area() {
        return 3.14;
    }
};

struct Point {
    double x;
    double y;
};

}

void greet() {
    std::cout << "hello" << std::endl;
}

int main() {
    geometry::Circle c;
    c.draw();
    greet();
    return 0;
}
