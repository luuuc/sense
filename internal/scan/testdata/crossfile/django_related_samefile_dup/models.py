class Ticket:
    event = models.ForeignKey(Event, related_name='things')

class Coupon:
    event = models.ForeignKey(Event, related_name='things')
