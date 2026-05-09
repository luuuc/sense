# Nested describe with constant and method string
describe TopicCreator do
  describe "#create" do
    it "creates a valid topic" do
      topic = TopicCreator.create(user, guardian, attrs)
      expect(topic).to be_valid
    end
  end
end

# Top-level it block (no enclosing describe)
it "works standalone" do
  result = process_data
  expect(result).to be_ok
end

# Context blocks with string descriptions
describe Order do
  context "when pending" do
    it "calculates total" do
      order = Order.new
      order.calculate_total
    end
  end
end

# before/after/around hooks — file-level fallback (no string description)
describe User do
  before do
    setup_database
  end

  after do
    teardown_database
  end

  it "validates email" do
    user = User.new(email: "test@example.com")
    user.valid?
  end
end

# let with symbol arg — file-level fallback
describe Post do
  let(:author) do
    FactoryBot.create(:user)
  end

  it "publishes" do
    post = Post.new(author: author)
    post.publish
  end
end

# Shared examples — file-level fallback (it_behaves_like has no block here)
describe Comment do
  it_behaves_like "a votable"
end

# Interpolated description — file-level fallback
describe Product do
  it "creates a #{thing}" do
    product = Product.new
    product.save
  end
end

# Deeply nested blocks (depth > 3 triggers fallback)
describe Category do
  describe "#index" do
    context "when admin" do
      describe "with filters" do
        it "returns filtered" do
          Category.filtered_list
        end
      end
    end
  end
end

# Brace block syntax (expect { ... })
describe Item do
  it "raises on invalid" do
    expect {
      Item.process!(nil)
    }.to raise_error(ArgumentError)
  end
end

# Mixed call and identifier inside test block
describe Notification do
  it "sends email" do
    notify_user
    Notification.deliver
  end
end
