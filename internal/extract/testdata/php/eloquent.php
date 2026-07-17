<?php
namespace App\Models;

use App\Observers\OrderObserver;

#[ObservedBy([OrderObserver::class])]
class Order extends Model {
    public function items() {
        return $this->hasMany(OrderItem::class);
    }

    public function customer() {
        return $this->belongsTo(Customer::class);
    }

    public function scopeShipped($query) {
        return $query->whereNotNull('shipped_at');
    }
}
