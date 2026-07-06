class OrderPosition:
    order = models.ForeignKey(Order, related_name='all_positions')
