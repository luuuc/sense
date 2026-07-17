<?php
namespace App\Services;

use App\Models\Order;
use App\Contracts\Gateway as PayGateway;
use App\Support\Logger;

#[Injectable]
class Checkout extends BaseService implements Chargeable {
    use Billable, \App\Concerns\Notifiable;

    private Logger $log;

    public function __construct(private PayGateway $gateway) {}

    public static function make(): static {
        return new static();
    }

    #[Route("/checkout")]
    protected function process(Order $order): void {
        $tax = new TaxCalculator();
        $tax->rate($order);
        $order->total();
        $this->log->info("processing");
        $this->gateway->charge(10);
        $this->finalize();
        self::make();
        parent::boot();
        Order::query();
        $unknown->save();
        $unknown->finalizeLater();
        $unknown->{$dynamic}();
        format_amount(10);
        \App\Support\helper();
    }

    private function finalize(): void {}
}

trait Billable {
    public function bill(): void {}
}

enum Status: string {
    case Active = 'active';
}

function format_amount(int $cents): string {
    return (string) $cents;
}
