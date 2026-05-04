interface Repository<T> {
  find(id: string): T;
  save(entity: T): void;
}

interface CachedRepository<T> extends Repository<T> {
  invalidate(id: string): void;
}

class UserRepository implements Repository<User> {
  find(id: string): User { return new User(); }
  save(entity: User): void {}
}

class CachedUserRepository extends UserRepository implements CachedRepository<User> {
  invalidate(id: string): void {}
  lookup(id: string): User {
    return this.find(id);
  }
}

class User {
  name: string;
}

interface Comparable<T> {
  compareTo(other: T): number;
}

type EntityMap<T> = Record<string, T> & Comparable<T>;
