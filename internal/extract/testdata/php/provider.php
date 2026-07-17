<?php
namespace App\Providers;

use App\Contracts\Gateway;
use App\Payments\StripeGateway;
use Illuminate\Support\Facades\Facade;
use App\Services\PaymentService;

class AppServiceProvider extends ServiceProvider {
    public function register(): void {
        $this->app->bind(Gateway::class, StripeGateway::class);
        $this->app->singleton('metrics', \App\Support\Metrics::class);
    }
}

class Payments extends Facade {
    protected static function getFacadeAccessor(): string {
        return PaymentService::class;
    }
}

class CheckoutController {
    public function store(): void {
        $gateway = app(Gateway::class);
        $gateway->charge(100);
        Payments::refund(5);
    }
}
