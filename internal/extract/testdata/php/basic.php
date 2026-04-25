<?php
namespace App\Models;

use App\Services\Logger;

class Animal {
    public function __construct(private string $name) {
    }

    public function speak(): void {
        echo "hello\n";
    }
}

class Dog extends Animal {
    public function speak(): void {
        echo "woof\n";
    }
}

interface Serializable {
    public function serialize(): string;
}

function greet(string $name): void {
    echo "Hello, $name\n";
}
