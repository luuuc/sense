#include <stdio.h>
#include <stdlib.h>

struct Point {
    int x;
    int y;
};

enum Color {
    RED,
    GREEN,
    BLUE
};

void greet(const char* name) {
    printf("Hello, %s\n", name);
}

int add(int a, int b) {
    return a + b;
}

int main() {
    greet("world");
    int result = add(1, 2);
    printf("result: %d\n", result);
    return 0;
}
