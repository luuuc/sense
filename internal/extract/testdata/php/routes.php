<?php

use App\Http\OrderController;
use App\Events\OrderShipped;
use App\Listeners\SendShipmentNotification;

Route::get('/orders', [OrderController::class, 'index'])->middleware('auth');
Route::post('/orders', 'OrderController@store');

class EventServiceProvider {
    protected $listen = [
        OrderShipped::class => [SendShipmentNotification::class],
    ];
}
